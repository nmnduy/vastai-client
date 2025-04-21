package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/nmnduy/vastai-client/internal/grpc" // Import the generated protobuf code

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	// workerStatusCreated represents the initial status when a worker starts.
	workerStatusCreated = "created"
	// workerStatusAuthenticated represents the status after successful authentication.
	workerStatusAuthenticated = "authenticated"
	// workerStatusRunning represents the status when the worker is actively processing jobs.
	workerStatusRunning = "running"
	// workerStatusStopped represents the status when the worker is shutting down.
	workerStatusStopped = "stopped"
)

var (
	serverAddr  = flag.String("addr", "localhost:50051", "The server address in the format host:port")
	workerToken string
)

func main() {
	flag.Parse()

	// Get authentication token from environment variable
	workerToken = os.Getenv("WORKER_AUTH_TOKEN")
	if workerToken == "" {
		log.Fatal("WORKER_AUTH_TOKEN environment variable not set")
	}

	// Set up a connection to the server.
	// TODO: Implement secure credentials (TLS) for production environments.
	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	client := pb.NewWorkerServiceClient(conn)
	log.Printf("Connected to server at %s", *serverAddr)

	// Create a context that can be cancelled on shutdown signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutdown signal received, cancelling context...")
		cancel() // Cancel the main context
	}()

	// 1. Authenticate the worker
	if !authenticate(ctx, client) {
		log.Fatal("Worker authentication failed. Exiting.")
		return // Exit if authentication fails
	}
	log.Println("Worker authenticated successfully.")

	// 2. Start the main worker loop
	runWorkerLoop(ctx, client)

	log.Println("Worker loop finished. Exiting.")
}

// authenticate attempts to authenticate the worker with the server.
func authenticate(ctx context.Context, client pb.WorkerServiceClient) bool {
	req := &pb.AuthenticateWorkerRequest{Token: workerToken}
	log.Printf("Attempting to authenticate worker with token prefix: %s...", getTokenPrefix(workerToken))

	authCtx, cancel := context.WithTimeout(ctx, 10*time.Second) // Timeout for authentication
	defer cancel()

	resp, err := client.AuthenticateWorker(authCtx, req)
	if err != nil {
		log.Printf("Authentication request failed: %v", err)
		return false
	}

	if !resp.Authenticated {
		log.Println("Server rejected authentication token.")
		return false
	}

	return true
}

// runWorkerLoop continuously fetches and processes jobs until the context is cancelled.
func runWorkerLoop(ctx context.Context, client pb.WorkerServiceClient) {
	log.Println("Starting worker loop...")
	ticker := time.NewTicker(5 * time.Second) // Poll for jobs every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, stopping worker loop.")
			return // Exit loop if context is cancelled
		case <-ticker.C:
			// Try to fetch and process one job
			jobAvailable, err := fetchAndProcessJob(ctx, client)
			if err != nil {
				log.Printf("Error fetching/processing job: %v. Will retry.", err)
				// Optional: Implement backoff strategy here
			} else if !jobAvailable {
				log.Println("No job available currently. Will check again.")
			}
			// If a job was processed successfully (jobAvailable=true, err=nil), the loop continues immediately for the next tick.
		}
	}
}

// fetchAndProcessJob tries to get a job, process it, and submit the result.
// Returns true if a job was found and processed (or attempted), false if no job was available.
func fetchAndProcessJob(ctx context.Context, client pb.WorkerServiceClient) (jobAvailable bool, err error) {
	// Use the GetNextJob RPC to ask the server for the next available job for this authenticated worker.
	req := &pb.GetNextJobRequest{} // No parameters needed for this request currently
	log.Println("Requesting next available job via GetNextJob...")

	getJobCtx, cancel := context.WithTimeout(ctx, 15*time.Second) // Timeout for getting a job
	defer cancel()

	resp, err := client.GetNextJob(getJobCtx, req) // <-- USE GetNextJob HERE
	if err != nil {
		// Check if the error is 'NotFound' indicating no job is available, as per the proto definition.
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.NotFound {
			return false, nil // No job available is not an error for the loop
		}
		// Other errors during the RPC call
		return false, fmt.Errorf("failed to get next job: %w", err)
	}

	// Check if a valid job was returned (e.g., non-zero JobId)
	// The server should return a non-nil response with JobId=0 or empty input if no job is ready.
	// Or more likely, return the NotFound error handled above.
	if resp == nil || resp.JobId == 0 || resp.Input == "" {
		// This case might be redundant if the server correctly returns NotFound,
		// but it's safe to keep as a fallback.
		log.Println("Server indicated no job available (empty response).")
		return false, nil // No job available
	}

	log.Printf("Received job ID: %d", resp.JobId)
	jobAvailable = true // A job was received

	// Process the job
	// Use a separate context for job processing itself, potentially with its own timeout
	// Link it to the main context so cancellation propagates
	jobProcessCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()

	result, processErr := processJob(jobProcessCtx, resp.JobId, resp.Input)

	// Submit the result
	submitCtx, submitCancel := context.WithTimeout(ctx, 10*time.Second) // Timeout for submitting result
	defer submitCancel()

	submitReq := &pb.SubmitJobResultRequest{
		JobId:  resp.JobId,
		Result: result,
	}
	if processErr != nil {
		log.Printf("Job %d failed: %v", resp.JobId, processErr)
		submitReq.Error = processErr.Error() // Assign error message
	} else {
		log.Printf("Job %d completed successfully.", resp.JobId)
	}

	_, submitErr := client.SubmitJobResult(submitCtx, submitReq)
	if submitErr != nil {
		// Log the submission error, but the original processing error (if any) is more important
		log.Printf("Failed to submit result for job %d: %v", resp.JobId, submitErr)
		// Decide if this should be returned as the primary error.
		// If submission fails, the server might not know the job finished/failed.
		// Retrying submission might be necessary in a robust implementation.
		return jobAvailable, fmt.Errorf("failed to submit job result for job %d: %w (processing error: %v)", resp.JobId, submitErr, processErr)
	}

	log.Printf("Successfully submitted result for job %d", resp.JobId)
	return jobAvailable, processErr // Return the error from processing, if any
}

// processJob simulates the actual work being done for a job.
// Replace this with your actual job processing logic.
func processJob(ctx context.Context, jobID int64, input string) (result string, err error) {
	log.Printf("Starting processing job ID: %d, Input: %s", jobID, input)

	// Simulate work with a sleep, checking for context cancellation
	select {
	case <-time.After(10 * time.Second): // Simulate 10 seconds of work
		// Simulate potential errors based on input or other factors
		if input == "fail_me" {
			return "", fmt.Errorf("job %d failed intentionally based on input", jobID)
		}
		log.Printf("Finished processing job ID: %d", jobID)
		return fmt.Sprintf("Result for job %d with input '%s'", jobID, input), nil
	case <-ctx.Done():
		log.Printf("Job %d processing cancelled.", jobID)
		return "", ctx.Err() // Return context error (e.g., context.Canceled)
	}
}

// getTokenPrefix returns the first few characters of a token for logging, avoiding exposing the full token.
func getTokenPrefix(token string) string {
	prefixLen := 8
	if len(token) < prefixLen {
		prefixLen = len(token)
	}
	if prefixLen <= 0 {
		return ""
	}
	return token[:prefixLen]
}
