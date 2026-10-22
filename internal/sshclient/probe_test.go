package sshclient

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestProbeTCPOnline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	res := ProbeTCP(context.Background(), ln.Addr().String(), 2*time.Second)
	if !res.Online {
		t.Fatalf("expected online, got %+v", res)
	}
	if res.Latency <= 0 {
		t.Fatalf("latency = %v", res.Latency)
	}
	if ProbeError(res) != "" {
		t.Fatalf("online probe should have no error string")
	}
}

func TestProbeTCPOffline(t *testing.T) {
	// Reserve a port then close it so nothing is listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	res := ProbeTCP(context.Background(), addr, time.Second)
	if res.Online {
		t.Fatalf("expected offline, got %+v", res)
	}
	if res.Err == "" {
		t.Fatal("expected an error string")
	}
	if ProbeError(res) == "" {
		t.Fatal("ProbeError should describe the failure")
	}
}
