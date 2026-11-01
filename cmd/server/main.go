package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"redis-from-scratch/internal/command"
	"redis-from-scratch/internal/network"
	"redis-from-scratch/internal/store"
)

func main() {
	addr := flag.String("addr", ":6379", "listen address")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := network.New(command.New(store.New()))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	closed := make(chan struct{})
	go func() {
		<-sig
		srv.Close()
		close(closed)
	}()

	log.Printf("listening on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil {
		log.Fatal(err)
	}
	<-closed
}
