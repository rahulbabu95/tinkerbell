package dhcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
)

// DHCPv6Data holds data extracted from a v1alpha2 Hardware for DHCPv6 responses.
type DHCPv6Data struct {
	// MACAddress is the hardware address of the interface.
	MACAddress net.HardwareAddr
	// IPAddress is the IPv6 address to assign.
	IPAddress netip.Addr
	// PrefixLength is the subnet prefix length (e.g. 64).
	PrefixLength int
	// Gateway is the IPv6 gateway address.
	Gateway netip.Addr
	// Nameservers are IPv6 DNS servers.
	Nameservers []net.IP
	// DomainSearchList are DNS search suffixes.
	DomainSearchList []string
	// PreferredLifetime for IA_NA addresses.
	PreferredLifetime time.Duration
	// ValidLifetime for IA_NA addresses.
	ValidLifetime time.Duration
	// BootFileURL is the boot file URL (DHCPv6 option 59).
	BootFileURL string
	// Disabled indicates that DHCPv6 should not respond for this interface.
	Disabled bool
	// AllowNetboot indicates whether netbooting is allowed.
	AllowNetboot bool
}

const (
	defaultPreferredLifetime = 3600 * time.Second
	defaultValidLifetime     = 7200 * time.Second
	defaultPrefixLength      = 64
)

// ConvertV2ForDHCPv6 converts a v1alpha2 Hardware and MAC into DHCPv6-relevant data.
func ConvertV2ForDHCPv6(_ context.Context, mac net.HardwareAddr, hw *v1alpha2.Hardware) (*DHCPv6Data, error) {
	if hw == nil {
		return nil, errors.New("hardware is nil")
	}

	macStr := v1alpha2.MAC(mac.String())
	ni, ok := hw.Spec.NetworkInterfaces[macStr]
	if !ok {
		return nil, fmt.Errorf("no network interface found for MAC %s", mac.String())
	}

	d := &DHCPv6Data{
		MACAddress:        mac,
		PreferredLifetime: defaultPreferredLifetime,
		ValidLifetime:     defaultValidLifetime,
		PrefixLength:      defaultPrefixLength,
		AllowNetboot:      true, // default to allow netboot
	}

	// Check DHCP.IPv6 configuration.
	if ni.DHCP != nil && ni.DHCP.IPv6 != nil {
		dhcpv6 := ni.DHCP.IPv6
		d.Disabled = dhcpv6.Disabled

		// Nameservers
		for _, ns := range dhcpv6.Nameservers {
			ip := net.ParseIP(string(ns))
			if ip != nil && ip.To4() == nil {
				// Only include IPv6 nameservers.
				d.Nameservers = append(d.Nameservers, ip)
			}
		}

		// Domain search list
		d.DomainSearchList = dhcpv6.DomainSearchList

		// Lifetimes
		if dhcpv6.PreferredLifetime != nil {
			d.PreferredLifetime = time.Duration(*dhcpv6.PreferredLifetime) * time.Second
		}
		if dhcpv6.ValidLifetime != nil {
			d.ValidLifetime = time.Duration(*dhcpv6.ValidLifetime) * time.Second
		}

		// Boot file URL from CRD
		d.BootFileURL = dhcpv6.BootFileURL
	}

	// Extract IPAM.IPv6 for address/prefix/gateway.
	if ni.IPAM != nil && ni.IPAM.IPv6 != nil {
		ipv6 := ni.IPAM.IPv6

		if ipv6.Address != "" {
			addr, err := netip.ParseAddr(ipv6.Address)
			if err != nil {
				return nil, fmt.Errorf("failed to parse IPv6 address %q: %w", ipv6.Address, err)
			}
			if !addr.Is6() {
				return nil, fmt.Errorf("address %q is not a valid IPv6 address", ipv6.Address)
			}
			d.IPAddress = addr
		}

		if ipv6.Prefix != "" {
			prefix, err := strconv.Atoi(ipv6.Prefix)
			if err != nil {
				return nil, fmt.Errorf("failed to parse prefix length %q: %w", ipv6.Prefix, err)
			}
			if prefix < 1 || prefix > 128 {
				return nil, fmt.Errorf("invalid IPv6 prefix length %d", prefix)
			}
			d.PrefixLength = prefix
		}

		if ipv6.Gateway != "" {
			gw, err := netip.ParseAddr(ipv6.Gateway)
			if err != nil {
				return nil, fmt.Errorf("failed to parse gateway %q: %w", ipv6.Gateway, err)
			}
			d.Gateway = gw
		}
	}

	// Extract Netboot configuration.
	if ni.Netboot != nil {
		d.AllowNetboot = !ni.Netboot.Disabled
	}

	return d, nil
}
