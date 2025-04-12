package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	dbpkg "github.com/nmnduy/vastai-client/internal/db"
	pb "github.com/nmnduy/vastai-client/internal/grpc"
)

type WorkerServiceServer struct {
	pb.UnimplementedWorkerServiceServer
	db *dbpkg.DB
}

func NewWorkerServiceServer(db *dbpkg.DB) *WorkerServiceServer {
	return &WorkerServiceServer{db: db}
}

func (s *WorkerServiceServer) AuthenticateWorker(ctx context.Context, req *pb.AuthenticateWorkerRequest) (*pb.AuthenticateWorkerResponse, error) {
	token := req.GetToken()

	authToken, err := s.db.GetWorkerAuthToken(ctx, token)
	if err != nil {
		log.Printf("Error getting worker auth token: %v", err)
		return &pb.AuthenticateWorkerResponse{Authenticated: false}, fmt.Errorf("failed to authenticate worker")
	}

	if authToken == nil {
		log.Printf("Worker auth token not found: %s", token)
		return &pb.AuthenticateWorkerResponse{Authenticated: false}, fmt.Errorf("worker auth token not found")
	}

	log.Printf("Worker authenticated successfully: %s", token)
	return &pb.AuthenticateWorkerResponse{Authenticated: true}, nil
}

func (s *WorkerServiceServer) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	jobID := req.GetJobId()
	jobIDStr := fmt.Sprintf("%d", jobID)

	jobStatus, err := s.db.GetJobStatus(ctx, jobIDStr)
	if err != nil {
		log.Printf("Error getting job status: %v", err)
		return nil, fmt.Errorf("failed to get job")
	}

	if jobStatus == nil {
		log.Printf("Job not found: %d", jobID)
		return nil, fmt.Errorf("job not found")
	}

	if !jobStatus.Input.Valid {
		log.Printf("Job input is nil: %d", jobID)
		return nil, fmt.Errorf("job input is nil")
	}

	return &pb.GetJobResponse{JobId: jobID, Input: jobStatus.Input.String}, nil
}

func (s *WorkerServiceServer) SubmitJobResult(ctx context.Context, req *pb.SubmitJobResultRequest) (*pb.SubmitJobResultResponse, error) {
	jobID := req.GetJobId()
	result := req.GetResult()
	errorMsg := req.GetError()
	jobIDStr := fmt.Sprintf("%d", jobID)

	err := s.db.UpdateJobStatus(ctx, jobIDStr, "completed", &errorMsg, &result)
	if err != nil {
		log.Printf("Error updating job status: %v", err)
		return &pb.SubmitJobResultResponse{Success: false}, fmt.Errorf("failed to update job status")
	}

	log.Printf("Job result submitted successfully: %d", jobID)
	return &pb.SubmitJobResultResponse{Success: true}, nil
}

func ServeGRPC(db *dbpkg.DB) error {
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051" // Default gRPC port
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
		return err
	}

	s := grpc.NewServer()
	pb.RegisterWorkerServiceServer(s, NewWorkerServiceServer(db))
	log.Printf("server listening at %v", lis.Addr())

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
		return err
	}

	return nil
}
