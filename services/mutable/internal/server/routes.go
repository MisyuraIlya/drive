package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mutable/internal/database"
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"github.com/minio/minio-go/v7"
)

func (s *Server) RegisterRoutes() http.Handler {
	r := httprouter.New()

	corsWrapper := s.corsMiddleware(r)
	r.HandlerFunc(http.MethodGet, "/", s.HelloWorldHandler)
	r.HandlerFunc(http.MethodGet, "/health", s.healthHandler)
	r.HandlerFunc(http.MethodPost, "/uploadFile", s.uploadHandler)
	r.HandlerFunc(http.MethodDelete, "/deleteFile", s.deleteHandler)
	return corsWrapper
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Credentials", "false")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) HelloWorldHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{"message": "Hello World"}
	jsonResp, err := json.Marshal(resp)
	if err != nil {
		log.Fatalf("error marshaling JSON: %v", err)
	}
	w.Write(jsonResp)
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	jsonResp, err := json.Marshal(s.db.Health())
	if err != nil {
		log.Fatalf("error marshaling JSON: %v", err)
	}
	w.Write(jsonResp)
}

const (
	maxUploadSize = 100 * 1024 * 1024 * 1024 // 100 GiB
	maxMemory     = 32 << 20                 // 32 MiB
	bucketName    = "uploads"
)

// uploadHandler handles file uploads, deduplicates by content, and stores references.
func (s *Server) uploadHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Enforce total upload size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// 2. Parse the multipart form
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		http.Error(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 3. Validate user_id and name
	userID := r.FormValue("user_id")
	name := r.FormValue("name")
	if userID == "" || name == "" {
		http.Error(w, "user_id and name are required", http.StatusBadRequest)
		return
	}

	// 4. Get file part
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 5. Read file into memory (for hashing & upload)
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "failed to read file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 6. Compute SHA-256 hash for deduplication
	sum := sha256.Sum256(data)
	fileHash := hex.EncodeToString(sum[:])

	ctx := r.Context()
	db := s.db.(database.Service).DB()

	// 7. Check for existing file by hash
	var existingID int
	err = db.QueryRowContext(ctx,
		"SELECT id FROM files WHERE file_hash = $1", fileHash,
	).Scan(&existingID)

	if err == nil {
		// duplicate: ensure we don't insert multiple refs/originals for the same user
		var existingRefID int
		refErr := db.QueryRowContext(ctx,
			`SELECT id FROM files
		      WHERE user_id = $1
		        AND (file_hash = $2 OR parent_file_id = $3)`,
			userID, fileHash, existingID,
		).Scan(&existingRefID)

		if refErr == nil {
			// User already has a record for this content
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"message":"Already uploaded","file_id":%d}`, existingRefID)
			return
		} else if refErr != sql.ErrNoRows {
			http.Error(w, "database error: "+refErr.Error(), http.StatusInternalServerError)
			return
		}

		// insert new reference row WITHOUT the file_hash
		var refID int
		err = db.QueryRowContext(ctx, `
		    INSERT INTO files (name, user_id, parent_file_id, type, ready)
		    VALUES ($1, $2, $3, $4, $5)
		    RETURNING id`,
			name, userID, existingID, "FILE", true,
		).Scan(&refID)
		if err != nil {
			http.Error(w, "failed to record duplicate reference: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"message":"Duplicate reference created","file_id":%d}`, refID)
		return

	} else if err != sql.ErrNoRows {
		// some other DB error
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 8. No duplicate: insert a new file record (not yet ready)
	var newID int
	err = db.QueryRowContext(ctx,
		`INSERT INTO files (name, user_id, type, ready, file_hash)
	     VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		name, userID, "FILE", false, fileHash,
	).Scan(&newID)
	if err != nil {
		http.Error(w, "failed to create metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	objectName := strconv.Itoa(newID)

	// 9. Upload to MinIO
	reader := bytes.NewReader(data)
	_, err = s.s3.PutObject(ctx, bucketName, objectName, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: header.Header.Get("Content-Type"),
	})
	if err != nil {
		// Rollback metadata if upload fails
		db.ExecContext(ctx, "DELETE FROM files WHERE id = $1", newID)
		http.Error(w, "failed to store file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 10. Mark file as ready
	_, err = db.ExecContext(ctx, "UPDATE files SET ready = true WHERE id = $1", newID)
	if err != nil {
		log.Printf("warning: failed to mark ready: %v", err)
	}

	// 11. Record single chunk (index 0)
	_, err = db.ExecContext(ctx,
		`INSERT INTO chunks (user_id, file_id, chunk_hash, index)
		 VALUES ($1,$2,$3,$4)`, userID, newID, fileHash, 0,
	)
	if err != nil {
		log.Printf("warning: failed to record chunk: %v", err)
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"message":"File uploaded","file_id":%d}`, newID)
}

func (s *Server) deleteHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	idStr := r.URL.Query().Get("file_id")
	if userID == "" || idStr == "" {
		http.Error(w, "user_id and file_id are required", http.StatusBadRequest)
		return
	}
	fileID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid file_id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	db := s.db.(database.Service).DB()

	// Find parent reference
	var parent sql.NullInt64
	err = db.QueryRowContext(ctx,
		"SELECT parent_file_id FROM files WHERE id = $1 AND user_id = $2",
		fileID, userID,
	).Scan(&parent)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "file not found", http.StatusNotFound)
		} else {
			http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// If it is a duplicate reference, just remove it
	if parent.Valid {
		_, err = db.ExecContext(ctx, "DELETE FROM files WHERE id = $1", fileID)
		if err != nil {
			http.Error(w, "failed to delete reference: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"message":"Reference deleted"}`)
		return
	}

	// Original file: check for other references
	var refCount int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM files WHERE parent_file_id = $1", fileID,
	).Scan(&refCount)
	if err != nil {
		http.Error(w, "database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete metadata record
	_, err = db.ExecContext(ctx, "DELETE FROM files WHERE id = $1", fileID)
	if err != nil {
		http.Error(w, "failed to delete metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// If still referenced by others, keep storage
	if refCount > 0 {
		fmt.Fprintf(w, `{"message":"Record deleted; %d references remain, keeping content."}`, refCount)
		return
	}

	// No more references: clean up storage and chunks
	objectName := strconv.Itoa(fileID)
	if err := s.s3.RemoveObject(ctx, bucketName, objectName, minio.RemoveObjectOptions{}); err != nil {
		log.Printf("minio remove error: %v", err)
		http.Error(w, "failed to delete storage: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = db.ExecContext(ctx, "DELETE FROM chunks WHERE file_id = $1", fileID)
	if err != nil {
		log.Printf("warning: failed to delete chunks: %v", err)
	}

	fmt.Fprint(w, `{"message":"File and chunks deleted"}`)
}
