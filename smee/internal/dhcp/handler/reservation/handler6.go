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
	"github.com/insomniacslk/dhcp/rfc1035label"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/smee/internal/dhcp"
)

// Handler6 handles DHCPv6 messages for IPv6 host reservations.
type Handler6 struct {
	// Backend is the v1alpha2 backend to use for getting DHCP data.
	// If nil, BackendV1 is used as a fallback.
	Backend BackendReaderV2

	// BackendV1 is the v1alpha1 fallback backend.
	BackendV1 BackendReader

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

	mac := h.extractMAC(m, peer)
	if mac == nil {
		h.Log.Info("could not extract MAC from DHCPv6 message, ignoring", "peer", peer.String())
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
	case dhcpv6.MessageTypeInformationRequest:
		log.Info("received DHCPv6 Information-Request")
		h.handleInformationRequest(conn, peer, m, mac, log)
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

func (h *Handler6) handleInformationRequest(conn net.PacketConn, peer net.Addr, msg *dhcpv6.Message, mac net.HardwareAddr, log logr.Logger) {
	resp, err := h.buildInfoReply(msg, mac, log)
	if err != nil {
		log.Error(err, "failed to build Information-Request Reply")
		return
	}
	h.send(conn, peer, resp, log)
}

func (h *Handler6) buildInfoReply(req *dhcpv6.Message, mac net.HardwareAddr, log logr.Logger) (*dhcpv6.Message, error) {
	resp, err := dhcpv6.NewMessage()
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCPv6 message: %w", err)
	}
	resp.MessageType = dhcpv6.MessageTypeReply
	resp.TransactionID = req.TransactionID

	resp.AddOption(dhcpv6.OptServerID(h.ServerDUID))

	if cid := req.GetOneOption(dhcpv6.OptionClientID); cid != nil {
		resp.UpdateOption(cid)
	}

	// Boot File URL (option 59) — the main thing an Information-Request wants
	port := h.BootFilePort
	if port == 0 {
		port = 8080
	}
	if h.ServerAddr.IsValid() && !h.ServerAddr.IsUnspecified() {
		bootFileURL := fmt.Sprintf("http://[%s]:%d/ipxe/binary/ipxe.efi", h.ServerAddr.String(), port)
		if uc := req.Options.UserClasses(); len(uc) > 0 {
			for _, class := range uc {
				if string(class) == string(dhcp.Tinkerbell) {
					bootFileURL = fmt.Sprintf("http://[%s]:%d/ipxe/script/%s/auto.ipxe", h.ServerAddr.String(), port, mac.String())
					break
				}
			}
		}
		resp.AddOption(dhcpv6.OptBootFileURL(bootFileURL))
	}

	// DNS servers (option 23)
	if h.ServerAddr.IsValid() {
		ip := h.ServerAddr.As16()
		resp.AddOption(dhcpv6.OptDNS(net.IP(ip[:])))
	}

	log.Info("built DHCPv6 Information-Request Reply", "bootFileURL", fmt.Sprintf("http://[%s]:%d/ipxe/binary/ipxe.efi", h.ServerAddr.String(), port))
	return resp, nil
}

func (h *Handler6) buildResponse(req *dhcpv6.Message, mac net.HardwareAddr, msgType dhcpv6.MessageType, log logr.Logger) (*dhcpv6.Message, error) {
	ctx := context.Background()

	var hwData *dhcp.DHCPv6Data
	if h.Backend != nil {
		hw, err := h.Backend.FilterHardwareV2(ctx, data.HardwareFilter{ByMACAddress: mac.String()})
		if err == nil {
			hwData, err = dhcp.ConvertV2ForDHCPv6(ctx, mac, hw)
			if err != nil {
				log.V(1).Info("v2 conversion failed, trying v1 fallback", "error", err)
			}
		} else {
			log.V(1).Info("v2 backend lookup failed, trying v1 fallback", "error", err)
		}
	}
	if hwData == nil && h.BackendV1 != nil {
		hw, err := h.BackendV1.FilterHardware(ctx, data.HardwareFilter{ByMACAddress: mac.String()})
		if err != nil {
			return nil, fmt.Errorf("v1 backend lookup failed: %w", err)
		}
		hwData, err = dhcp.ConvertV1ForDHCPv6(ctx, mac, hw)
		if err != nil {
			return nil, fmt.Errorf("convert v1 for DHCPv6 failed: %w", err)
		}
	}
	if hwData == nil {
		return nil, fmt.Errorf("no backend configured or hardware not found for MAC %s", mac)
	}

	if hwData.Disabled {
		return nil, fmt.Errorf("DHCPv6 is disabled for MAC %s", mac)
	}

	if !hwData.IPAddress.IsValid() || !hwData.IPAddress.Is6() {
		return nil, fmt.Errorf("hardware %s does not have a valid IPv6 address", mac)
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
	ipv6Bytes := hwData.IPAddress.As16()
	iaAddr := &dhcpv6.OptIAAddress{
		IPv6Addr:          net.IP(ipv6Bytes[:]),
		PreferredLifetime: hwData.PreferredLifetime,
		ValidLifetime:     hwData.ValidLifetime,
	}

	// T1 = preferred/2, T2 = preferred*0.8
	t1 := hwData.PreferredLifetime / 2
	t2 := time.Duration(float64(hwData.PreferredLifetime) * 0.8)

	ianaOpt := &dhcpv6.OptIANA{
		IaId:    iaid,
		T1:      t1,
		T2:      t2,
		Options: dhcpv6.IdentityOptions{Options: dhcpv6.Options{iaAddr}},
	}
	resp.AddOption(ianaOpt)

	// DNS servers (option 23)
	if len(hwData.Nameservers) > 0 {
		resp.AddOption(dhcpv6.OptDNS(hwData.Nameservers...))
	}

	// Domain Search List (option 24)
	if len(hwData.DomainSearchList) > 0 {
		resp.AddOption(dhcpv6.OptDomainSearchList(&rfc1035label.Labels{
			Labels: hwData.DomainSearchList,
		}))
	}

	// Boot File URL (option 59)
	bootFileURL := hwData.BootFileURL
	if bootFileURL == "" && h.ServerAddr.IsValid() && !h.ServerAddr.IsUnspecified() {
		// Fall back to generating a boot file URL from ServerAddr.
		port := h.BootFilePort
		if port == 0 {
			port = 8080
		}
		bootFileURL = fmt.Sprintf("http://[%s]:%d/ipxe/binary/ipxe.efi", h.ServerAddr.String(), port)
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
	}
	if bootFileURL != "" && hwData.AllowNetboot {
		resp.AddOption(dhcpv6.OptBootFileURL(bootFileURL))
	}

	log.Info("built DHCPv6 response", "type", msgType.String(), "ipv6Addr", hwData.IPAddress.String(), "prefix", hwData.PrefixLength, "bootFileURL", bootFileURL, "allowNetboot", hwData.AllowNetboot)
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
func (h *Handler6) extractMAC(msg *dhcpv6.Message, peer net.Addr) net.HardwareAddr {
	// Try option 79 (Client Link-Layer Address Option, RFC 6939)
	if opt := msg.GetOneOption(dhcpv6.OptionClientLinkLayerAddr); opt != nil {
		optData := opt.ToBytes()
		// Format: 2 bytes hw type + N bytes link-layer addr
		if len(optData) >= 8 { // 2 byte hw type + 6 byte MAC
			return net.HardwareAddr(optData[2:8])
		}
	}

	// Try Client ID DUID-LL or DUID-LLT
	cidDUID := msg.Options.ClientID()
	if cidDUID != nil {
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
	}

	// Fallback: derive MAC from peer's IPv6 link-local address (EUI-64)
	// fe80::5054:ff:fea6:1 → MAC 52:54:00:a6:00:01
	if udpAddr, ok := peer.(*net.UDPAddr); ok && udpAddr.IP.IsLinkLocalUnicast() {
		ip := udpAddr.IP.To16()
		if ip != nil && len(ip) == 16 {
			// EUI-64: bytes 8-15 of the IPv6 address, with bit 6 of byte 8 flipped
			mac := make(net.HardwareAddr, 6)
			mac[0] = ip[8] ^ 0x02 // flip universal/local bit
			mac[1] = ip[9]
			mac[2] = ip[10]
			// skip ip[11] and ip[12] which are 0xff, 0xfe in EUI-64
			mac[3] = ip[13]
			mac[4] = ip[14]
			mac[5] = ip[15]
			return mac
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
