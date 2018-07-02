package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"
	"zues/kube"
	pb "zues/proto"
	"zues/rpc"
	"zues/server"

	"github.com/kataras/golog"
	"google.golang.org/grpc"

	"github.com/soheilhy/cmux"
)

// Replaced to approprate value with -ldflags
var version = "development"

func main() {
	fmt.Printf("running version %s\n", version)
	// Docker needs to use the 0.0.0.0 format to forward all requests
	// to the server in the container
	var networkString = "localhost:8284"
	if os.Getenv("DOCKER_ENV") == "true" {
		networkString = "0.0.0.0:8284"
	}
	listener, err := net.Listen("tcp", networkString)
	if err != nil {
		panic(err)
	}

	startWatch := make(chan struct{})

	// Start the K8s session
	kube.Session = kube.New()
	// Start watching after servers startup
	go kube.Session.WatchPodEvents(startWatch)

	m := cmux.New(listener)
	grpcListener := m.Match(cmux.HTTP2HeaderField("content-type", "application/grpc"))
	httpListener := m.Match(cmux.HTTP1Fast())

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, os.Kill)

	go serveHTTPServer(httpListener)
	go servegRPCServer(grpcListener)

	go func() {
		select {
		case <-time.After(2 * time.Second):
			startWatch <- struct{}{}
		}
	}()

	<-c

	golog.Debug("Shutting down servers gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = server.ZuesServer.Application.Shutdown(ctx); err != nil {
		golog.Error("error while shutting down gracefully...")
		golog.Debug("falling back to hard terminate")
		os.Exit(1)
	}
}

func serveHTTPServer(l net.Listener) error {
	golog.Info("Starting HTTP server...")
	server.ZuesServer = server.New(nil, "", version)
	server.ZuesServer.Start(l)
	return nil
}

func servegRPCServer(l net.Listener) error {
	golog.Info("Starting gRPC server...")
	s := grpc.NewServer()
	pb.RegisterZuesControlServer(s, &rpc.GRPCServer{})
	return s.Serve(l)
}
