package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/charmbracelet/log"
	_ "github.com/lib/pq"
	"github.com/vinimdocarmo/quackfs/src/internal/logger"
	"github.com/vinimdocarmo/quackfs/src/internal/storage"
)

func main() {
	// Initialize logger first thing
	log := logger.New(os.Stderr)

	// Check if a subcommand was provided
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Extract the subcommand
	command := os.Args[1]

	// Remove the subcommand from os.Args to make flag parsing work correctly
	os.Args = slices.Delete(os.Args, 1, 2)

	// Connect to the database
	db := newDB(log)
	defer db.Close()

	// Set up S3 client
	s3Endpoint := getEnvOrDefault("AWS_ENDPOINT_URL", "http://localhost:4566")
	s3Region := getEnvOrDefault("AWS_REGION", "us-east-1")
	s3BucketName := getEnvOrDefault("S3_BUCKET_NAME", "quackfs-bucket")

	// Load AWS SDK configuration
	cfgOptions := []func(*config.LoadOptions) error{
		config.WithRegion(s3Region),
	}

	log.Debug("Using static credentials for LocalStack")
	cfgOptions = append(cfgOptions,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"test", "test", "test")))

	cfg, err := config.LoadDefaultConfig(context.Background(), cfgOptions...)
	if err != nil {
		log.Fatal("Failed to configure AWS client", "error", err)
	}

	// Create an S3 client with custom endpoint for LocalStack
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s3Endpoint)
		o.UsePathStyle = true // Required for LocalStack
		o.DisableLogOutputChecksumValidationSkipped = true
	})

	// Create a storage manager
	sm := storage.NewManager(db, s3Client, s3BucketName, log)
	defer sm.Close()

	// Execute the appropriate command
	switch command {
	case "write":
		executeWriteCommand(sm, log)
	case "read":
		executeReadCommand(sm, log)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

// printUsage prints the usage information for the CLI tool
func printUsage() {
	fmt.Println("Usage: op <command> [options]")
	fmt.Println("Commands:")
	fmt.Println("  write      - Write data to a file")
	fmt.Println("  read       - Read and print file content to standard output")
	fmt.Println("")
	fmt.Println("For detailed command usage:")
	fmt.Println("  op write -h")
	fmt.Println("  op read -h")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  op read -file myfile.txt")
	fmt.Println("  op read -file myfile.txt -version v1.0")
}

// executeWriteCommand handles the "write" subcommand
func executeWriteCommand(sm *storage.Manager, log *log.Logger) {
	// Define command-line flags for write command
	writeCmd := flag.NewFlagSet("write", flag.ExitOnError)
	fileName := writeCmd.String("file", "", "Target file to write to")
	offset := writeCmd.Uint64("offset", 0, "Offset in the file to start writing from")
	data := writeCmd.String("data", "", "ASCII data to write to the file")
	allowBeyondSize := writeCmd.Bool("allow-beyond-size", true, "Allow writing beyond current file size (fills gap with null bytes)")

	// Parse the flags
	writeCmd.Parse(os.Args[1:])

	// Validate required flags
	if *fileName == "" {
		log.Error("Missing required flag: -file")
		fmt.Println("Usage: op write -file <filename> -offset <offset> -data <data>")
		os.Exit(1)
	}

	if *data == "" {
		log.Error("Missing required flag: -data")
		fmt.Println("Usage: op write -file <filename> -offset <offset> -data <data>")
		os.Exit(1)
	}

	ctx := context.Background()

	// Check if the file exists
	fileID, err := sm.GetFileIDByName(ctx, *fileName)
	if err != nil {
		log.Fatal("Failed to check if file exists", "error", err)
	}

	// If the file doesn't exist, create it
	if fileID == 0 {
		log.Info("File does not exist, creating it", "fileName", *fileName)
		fileID, err = sm.InsertFile(ctx, *fileName)
		if err != nil {
			log.Fatal("Failed to create file", "error", err)
		}
	}

	// Get current file size
	fileSize := uint64(0)
	if fileID != 0 {
		fileSize, err = sm.SizeOf(ctx, *fileName)
		if err != nil {
			log.Fatal("Failed to get file size", "error", err)
		}
	}

	// If writing beyond file size and it's allowed, fill the gap with null bytes
	if *offset > fileSize && *allowBeyondSize {
		if *offset > fileSize {
			log.Info("Writing beyond file size, filling gap with null bytes",
				"fileName", *fileName,
				"currentSize", fileSize,
				"targetOffset", *offset)

			// Calculate the gap size
			gapSize := *offset - fileSize

			// Only fill the gap if it's not too large (prevent accidental huge allocations)
			if gapSize > 1024*1024*10 { // 10MB limit
				log.Fatal("Gap size too large, aborting", "gapSize", gapSize)
			}

			// Fill the gap with null bytes if needed
			if gapSize > 0 {
				nullBytes := make([]byte, gapSize)
				err := sm.WriteFile(ctx, *fileName, nullBytes, fileSize)
				if err != nil {
					log.Fatal("Failed to fill gap with null bytes", "error", err)
				}
				log.Info("Gap filled with null bytes", "gapSize", gapSize)
			}
		}
	}

	// Write the data to the file at the specified offset
	dataBytes := []byte(*data)
	err = sm.WriteFile(ctx, *fileName, dataBytes, *offset)
	if err != nil {
		log.Fatal("Failed to write data", "error", err)
	}

	log.Info("Data written successfully",
		"fileName", *fileName,
		"offset", *offset,
		"dataSize", len(dataBytes))

	fmt.Printf("Successfully wrote %d bytes to %s at offset %d\n", len(dataBytes), *fileName, *offset)
}

// executeReadCommand handles the "read" subcommand
func executeReadCommand(sm *storage.Manager, log *log.Logger) {
	// Define command-line flags for read command
	readCmd := flag.NewFlagSet("read", flag.ExitOnError)
	fileName := readCmd.String("file", "", "Target file to read from")
	offset := readCmd.Uint64("offset", 0, "Offset in the file to start reading from (default: 0)")
	size := readCmd.Uint64("size", 0, "Number of bytes to read (default: entire file)")
	version := readCmd.String("version", "", "Version tag to read from (default: latest)")

	// Parse the flags
	readCmd.Parse(os.Args[1:])

	// Validate required flags
	if *fileName == "" {
		log.Error("Missing required flag: -file")
		fmt.Println("Usage: op read -file <filename> [-offset <offset>] [-size <size>] [-version <tag>]")
		os.Exit(1)
	}

	ctx := context.Background()

	// Check if the file exists
	fileID, err := sm.GetFileIDByName(ctx, *fileName)
	if err != nil {
		log.Fatal("Failed to check if file exists", "error", err)
	}

	// If the file doesn't exist, report an error
	if fileID == 0 {
		log.Error("File does not exist", "fileName", *fileName)
		fmt.Printf("Error: File '%s' does not exist\n", *fileName)
		os.Exit(1)
	}

	// Get file size to determine how much to read if size is not specified
	fileSize, err := sm.SizeOf(ctx, *fileName)
	if err != nil {
		log.Fatal("Failed to get file size", "error", err)
	}

	// If size is 0, read the entire file from the offset
	readSize := *size
	if readSize == 0 {
		readSize = fileSize - *offset
	}

	var data []byte

	// Read the data from the file, using version-specific method if a version tag is provided
	if *version != "" {
		log.Info("Reading file content with version",
			"fileName", *fileName,
			"offset", *offset,
			"size", readSize,
			"version", *version)

		data, err = sm.ReadFile(ctx, *fileName, *offset, readSize, storage.WithVersion(*version))
		if err != nil {
			log.Fatal("Failed to read data with version", "error", err)
		}
	} else {
		log.Info("Reading file content",
			"fileName", *fileName,
			"offset", *offset,
			"size", readSize)

		data, err = sm.ReadFile(ctx, *fileName, *offset, readSize)
		if err != nil {
			log.Fatal("Failed to read data", "error", err)
		}
	}

	// Print file information
	if *version != "" {
		log.Info("Read file content with version",
			"fileName", *fileName,
			"offset", *offset,
			"size", readSize,
			"bytesRead", len(data),
			"version", *version)
	} else {
		log.Info("Read file content",
			"fileName", *fileName,
			"offset", *offset,
			"size", readSize,
			"bytesRead", len(data))
	}

	// Print the data to stdout
	fmt.Print(string(data))

	// If the output doesn't end with a newline, add one for better terminal display
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}

	log.Info("Read operation completed", "fileName", *fileName, "bytesRead", len(data))
}

// newDB creates a new database connection
func newDB(log *log.Logger) *sql.DB {
	host := getEnvOrDefault("POSTGRES_HOST", "localhost")
	port := getEnvOrDefault("POSTGRES_PORT", "5432")
	user := getEnvOrDefault("POSTGRES_USER", "postgres")
	password := getEnvOrDefault("POSTGRES_PASSWORD", "password")
	dbname := getEnvOrDefault("POSTGRES_DB", "quackfs")

	log.Debug("Using env vars", "host", host, "port", port, "user", user, "dbname", dbname)

	// Construct the connection string
	conn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", conn)
	if err != nil {
		log.Fatal("Failed to create database connection", "error", err)
	}

	// Test the connection
	err = db.Ping()
	if err != nil {
		log.Fatal("Failed to connect to database", "error", err)
	}

	log.Info("Connected to PostgreSQL database")
	return db
}

// getEnvOrDefault returns the environment variable value or a default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
