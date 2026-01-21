package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	streamMethod = "/agent.AgentService/Stream"
)

type Client struct {
	addr       string
	certFile   string
	keyFile    string
	caFile     string
	connectTTL time.Duration
}

func NewClient(addr, certFile, keyFile, caFile string) *Client {
	return &Client{
		addr:       addr,
		certFile:   certFile,
		keyFile:    keyFile,
		caFile:     caFile,
		connectTTL: 10 * time.Second,
	}
}

func (c *Client) Run(ctx context.Context) (func(map[string]any) error, <-chan map[string]any, error) {
	tlsConfig, err := c.tlsConfig()
	if err != nil {
		return nil, nil, err
	}
	creds := credentials.NewTLS(tlsConfig)
	dialCtx, cancel := context.WithTimeout(ctx, c.connectTTL)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, c.addr, grpc.WithTransportCredentials(creds), grpc.WithBlock())
	if err != nil {
		return nil, nil, fmt.Errorf("grpc dial: %w", err)
	}

	streamDesc := &grpc.StreamDesc{StreamName: "Stream", ServerStreams: true, ClientStreams: true}
	stream, err := conn.NewStream(ctx, streamDesc, streamMethod)
	if err != nil {
		return nil, nil, fmt.Errorf("grpc stream: %w", err)
	}

	sendCh := make(chan map[string]any, 128)
	recvCh := make(chan map[string]any, 128)

	go func() {
		defer close(recvCh)
		defer conn.Close()
		for {
			msg := new(structpb.Struct)
			if err := stream.RecvMsg(msg); err != nil {
				return
			}
			recvCh <- msg.AsMap()
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-sendCh:
				if msg == nil {
					continue
				}
				pb, err := structpb.NewStruct(msg)
				if err != nil {
					continue
				}
				_ = stream.SendMsg(pb)
			}
		}
	}()

	send := func(msg map[string]any) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sendCh <- msg:
			return nil
		}
	}

	return send, recvCh, nil
}

func (c *Client) tlsConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	caData, err := os.ReadFile(c.caFile)
	if err != nil {
		return nil, fmt.Errorf("read ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return nil, errors.New("append ca cert failed")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
