package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "internal/grpc"
)

func main() {
	// --- Configuration ---
	authToken := os.Getenv("WORKER_AUTH_TOKEN")
	if authToken == "" {
		log.Fatal("WORKER_AUTH_TOKEN environment variable not set")
	}

	serverAddr := os.Getenv("GRPC_SERVER_ADDRESS")
	if serverAddr == "" {
		serverAddr = "localhost:50051" // Default server address
	}

	jobIDStr := flag.String("job-id", "", "The ID of the job to fetch and execute")
	flag.Parse()

	if *jobIDStr == "" {
		log.Fatal("--job-id flag is required")
	}

	jobID, err := strconv.ParseInt(*jobIDStr, 10, 64)
	if err != nil {
		log.Fatalf("Invalid job-id: %v", err)
	}

	// --- gRPC Connection ---
	log.Printf("Connecting to gRPC server at %s", serverAddr)
	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials())) // Note: Using insecure credentials
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	defer conn.Close()

	client := pb.NewWorkerServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute) // Add a timeout for RPC calls
	defer cancel()

	// --- Authentication ---
	log.Printf("Authenticating worker...")
	authReq := &pb.AuthenticateWorkerRequest{Token: authToken}
	authResp, err := client.AuthenticateWorker(ctx, authReq)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}
	if !authResp.Authenticated {
		log.Fatal("Worker authentication rejected by server.")
	}
	log.Println("Worker authenticated successfully.")

	// --- Get Job ---
	log.Printf("Requesting job %d...", jobID)
	getJobReq := &pb.GetJobRequest{JobId: jobID}
	jobResp, err := client.GetJob(ctx, getJobReq)
	if err != nil {
		log.Fatalf("Failed to get job %d: %v", jobID, err)
	}
	log.Printf("Received job %d with input: %.50s...", jobResp.JobId, jobResp.Input) // Log truncated input

	// --- Simulate Job Execution ---
	log.Printf("Starting job %d...", jobID)
	// Replace this section with actual job processing logic
	time.Sleep(5 * time.Second) // Simulate work
	jobResult := "Successfully processed input: " + jobResp.Input
	jobError := "" // Assume success for now
	log.Printf("Finished job %d.", jobID)

	// --- Submit Result ---
	log.Printf("Submitting result for job %d...", jobID)
	submitReq := &pb.SubmitJobResultRequest{
		JobId:  jobID,
		Result: jobResult,
		Error:  jobError,
	}
	submitResp, err := client.SubmitJobResult(ctx, submitReq)
	if err != nil {
		log.Fatalf("Failed to submit job result for job %d: %v", jobID, err)
	}

	if submitResp.Success {
		log.Printf("Successfully submitted result for job %d.", jobID)
	} else {
		log.Printf("Server failed to accept result for job %d.", jobID)
	}

	log.Println("Worker finished.")
}
