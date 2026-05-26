package reservation

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/go-logr/logr"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
)

// Handler6 handles DHCPv6 messages for IPv6 host reservations.
type Handler6 struct {
	// Backend is the backend to use for getting DHCP data.
	Backend BackendReader

	// ServerAddr is smee's IPv6 address used for boot file URL.
	ServerAddr netip.Addr

	// Log is used to log messages.
	Log logr.Logger

	// ServerDUID is the server's DUID used in DHCPv6 responses.
	ServerDUID dhcpv6.DUID

	// BootFilePort is the HTTP port for iPXE binary/script serving.
	BootFilePort uint16
}

// Handle6 processes a DHCPv6 message and writes a response.
func (h *Handler6) Handle6(conn net.PacketConn, peer net.Addr, msg dhcpv6.DHCPv6) {
	if h.Log.GetSink() == nil {
		h.Log = logr.Discard()
	}

	m, err := msg.GetInnerMessage()
	if err != nil {
		h.Log.Error(err, "failed to get inner DHCPv6 message")
		return
	}

	mac := h.extractMAC(m)
	if mac == nil {
		h.Log.V(1).Info("could not extract MAC from DHCPv6 message, ignoring")
		return
	}

	log := h.Log.WithValues("mac", mac.String(), "xid", m.TransactionID.String(), "msgType", m.MessageType.String())

	switch m.MessageType {
	case dhcpv6.MessageTypeSolicit:
		log.Info("received DHCPv6 Solicit")
		h.handleSolicit(conn, peer, m, mac, log)
	case dhcpv6.MessageTypeRequest:
		log.Info("received DHCPv6 Request")
		h.handleRequest(conn, peer, m, mac, log)
	default:
		log.V(1).Info("ignoring DHCPv6 message type")
	}
}

func (h *Handler6) handleSolicit(conn net.PacketConn, peer net.Addr, msg *dhcpv6.Message, mac net.HardwareAddr, log logr.Logger) {
	resp, err := h.buildResponse(msg, mac, dhcpv6.MessageTypeAdvertise, log)
	if err != nil {
		log.Error(err, "failed to build Advertise")
		return
	}
	h.send(conn, peer, resp, log)
}

func (h *Handler6) handleRequest(conn net.PacketConn, peer net.Addr, msg *dhcpv6.Message, mac net.HardwareAddr, log logr.Logger) {
	resp, err := h.buildResponse(msg, mac, dhcpv6.MessageTypeReply, log)
	if err != nil {
		log.Error(err, "failed to build Reply")
		return
	}
	h.send(conn, peer, resp, log)
}

func (h *Handler6) buildResponse(req *dhcpv6.Message, mac net.HardwareAddr, msgType dhcpv6.MessageType, log logr.Logger) (*dhcpv6.Message, error) {
	ctx := context.Background()
	hw, err := h.Backend.FilterHardware(ctx, data.HardwareFilter{ByMACAddress: mac.String()})
	if err != nil {
		return nil, fmt.Errorf("backend lookup failed: %w", err)
	}

	hwData, err := dhcp.ConvertByMac(ctx, mac, hw)
	if err != nil {
		return nil, fmt.Errorf("convert by mac failed: %w", err)
	}

	if !hwData.DHCP.IPAddress.Is6() {
		return nil, fmt.Errorf("hardware %s does not have an IPv6 address", mac)
	}

	resp, err := dhcpv6.NewMessage()
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCPv6 message: %w", err)
	}
	resp.MessageType = msgType
	resp.TransactionID = req.TransactionID

	// Server ID
	resp.AddOption(dhcpv6.OptServerID(h.ServerDUID))

	// Client ID - echo back
	if cid := req.GetOneOption(dhcpv6.OptionClientID); cid != nil {
		resp.UpdateOption(cid)
	}

	// IA_NA with the assigned address
	iaid := [4]byte{0, 0, 0, 1}
	if reqIANA := req.Options.OneIANA(); reqIANA != nil {
		iaid = reqIANA.IaId
	}
	ipv6Bytes := hwData.DHCP.IPAddress.As16()
	iaAddr := &dhcpv6.OptIAAddress{
		IPv6Addr:          net.IP(ipv6Bytes[:]),
		PreferredLifetime: 3600 * time.Second,
		ValidLifetime:     7200 * time.Second,
	}
	ianaOpt := &dhcpv6.OptIANA{
		IaId:    iaid,
		T1:      1800 * time.Second,
		T2:      2880 * time.Second,
		Options: dhcpv6.IdentityOptions{Options: dhcpv6.Options{iaAddr}},
	}
	resp.AddOption(ianaOpt)

	// DNS servers (option 23)
	var dnsServers []net.IP
	for _, ns := range hwData.DHCP.NameServers {
		if ns.To16() != nil && ns.To4() == nil {
			// Only include IPv6 DNS servers
			dnsServers = append(dnsServers, ns)
		}
	}
	if len(dnsServers) > 0 {
		resp.AddOption(dhcpv6.OptDNS(dnsServers...))
	}

	// Boot File URL (option 59)
	if h.ServerAddr.IsValid() && !h.ServerAddr.IsUnspecified() {
		port := h.BootFilePort
		if port == 0 {
			port = 8080
		}
		bootFileURL := fmt.Sprintf("http://[%s]:%d/ipxe/binary/ipxe.efi", h.ServerAddr.String(), port)
		// Check if client already has iPXE (UserClass = "Tinkerbell")
		if uc := req.Options.UserClasses(); len(uc) > 0 {
			for _, class := range uc {
				if string(class) == string(dhcp.Tinkerbell) {
					// Client already has iPXE, serve the script instead
					bootFileURL = fmt.Sprintf("http://[%s]:%d/ipxe/script/%s/auto.ipxe", h.ServerAddr.String(), port, mac.String())
					break
				}
			}
		}
		resp.AddOption(dhcpv6.OptBootFileURL(bootFileURL))
	}

	log.Info("built DHCPv6 response", "type", msgType.String(), "ipv6Addr", hwData.DHCP.IPAddress.String())
	return resp, nil
}

func (h *Handler6) send(conn net.PacketConn, peer net.Addr, msg *dhcpv6.Message, log logr.Logger) {
	b := msg.ToBytes()
	if _, err := conn.WriteTo(b, peer); err != nil {
		log.Error(err, "failed to send DHCPv6 response")
	}
}

// extractMAC attempts to get the client's MAC address from the DHCPv6 message.
// It checks (in order):
// 1. Option 79 (Client Link-Layer Address)
// 2. Client ID DUID-LL
// 3. Client ID DUID-LLT
func (h *Handler6) extractMAC(msg *dhcpv6.Message) net.HardwareAddr {
	// Try option 79 (Client Link-Layer Address Option, RFC 6939)
	if opt := msg.GetOneOption(dhcpv6.OptionClientLinkLayerAddr); opt != nil {
		optData := opt.ToBytes()
		// Format: 2 bytes hw type + N bytes link-layer addr
		if len(optData) >= 8 { // 2 byte hw type + 6 byte MAC
			return net.HardwareAddr(optData[2:8])
		}
	}

	// Try Client ID
	cidDUID := msg.Options.ClientID()
	if cidDUID == nil {
		return nil
	}

	switch duid := cidDUID.(type) {
	case *dhcpv6.DUIDLL:
		if duid.HWType == iana.HWTypeEthernet && len(duid.LinkLayerAddr) == 6 {
			return duid.LinkLayerAddr
		}
	case *dhcpv6.DUIDLLT:
		if duid.HWType == iana.HWTypeEthernet && len(duid.LinkLayerAddr) == 6 {
			return duid.LinkLayerAddr
		}
	}

	return nil
}

// NewServerDUID creates a DUID-LLT for the server based on the given MAC address.
func NewServerDUID(mac net.HardwareAddr) dhcpv6.DUID {
	return &dhcpv6.DUIDLLT{
		HWType:        iana.HWTypeEthernet,
		Time:          dhcpv6.GetTime(),
		LinkLayerAddr: mac,
	}
}
