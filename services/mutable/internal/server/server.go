package server

import (
	"fmt"
	"github.com/minio/minio-go/v7"
	"log"
	"mutable/internal/S3"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"mutable/internal/database"
)

type Server struct {
	port int

	db database.Service
	s3 *minio.Client
}

func NewServer() *http.Server {
	port, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil {
		log.Fatalf("invalid PORT: %v", err)
	}

	dbSvc := database.New()

	fmt.Println("✅ Connected to database successfully")

	minioClient, err := S3.NewMinioClient()
	if err != nil {
		log.Fatalf("could not initialize S3 client: %v", err)
	}
	
	fmt.Println("✅ Connected to MinIO successfully")

	app := &Server{
		port: port,
		db:   dbSvc,
		s3:   minioClient,
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.port),
		Handler:      app.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server
}
