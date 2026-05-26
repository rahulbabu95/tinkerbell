package server

import (
	"context"
	"net"

	"github.com/go-logr/logr"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

// Handler6 is the interface for handling DHCPv6 messages.
type Handler6 interface {
	Handle6(conn net.PacketConn, peer net.Addr, msg dhcpv6.DHCPv6)
}

// DHCPv6Server represents a DHCPv6 server.
type DHCPv6Server struct {
	Conn     net.PacketConn
	Handlers []Handler6
	Logger   logr.Logger
}

// Serve listens for DHCPv6 messages and dispatches to handlers.
func (s *DHCPv6Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.Conn.Close()
	}()

	s.Logger.Info("DHCPv6 server listening", "addr", s.Conn.LocalAddr())

	for {
		buf := make([]byte, 4096)
		n, peer, err := s.Conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			s.Logger.Error(err, "error reading DHCPv6 packet")
			return err
		}

		msg, err := dhcpv6.FromBytes(buf[:n])
		if err != nil {
			s.Logger.V(1).Info("error parsing DHCPv6 message", "error", err)
			continue
		}

		for _, handler := range s.Handlers {
			go handler.Handle6(s.Conn, peer, msg)
		}
	}
}

// Close closes the underlying connection.
func (s *DHCPv6Server) Close() error {
	return s.Conn.Close()
}
