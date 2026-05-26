package kube

import (
	"context"
	"fmt"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// v1alpha2GVR is the GroupVersionResource for v1alpha2 Hardware.
var v1alpha2GVR = schema.GroupVersionResource{
	Group:    "tinkerbell.org",
	Version:  "v1alpha2",
	Resource: "hardware",
}

// AddV1Alpha2ToScheme registers v1alpha2 Hardware and HardwareList types in the given scheme.
// This is needed because the v1alpha2 tinkerbell package does not provide its own SchemeBuilder.
func AddV1Alpha2ToScheme(s *runtime.Scheme) error {
	gv := v1alpha2.GroupVersion
	s.AddKnownTypes(gv, &v1alpha2.Hardware{}, &v1alpha2.HardwareList{})
	metav1.AddToGroupVersion(s, gv)
	return nil
}

// FilterHardwareV2 looks up a single v1alpha2 Hardware object by MAC address.
// It lists all v1alpha2 Hardware objects and filters by the MAC key in NetworkInterfaces.
func (b *Backend) FilterHardwareV2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error) {
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "backend.kube.FilterHardwareV2")
	defer span.End()

	hwList := &v1alpha2.HardwareList{}
	listOpts := []client.ListOption{}
	if opts.InNamespace != "" {
		listOpts = append(listOpts, client.InNamespace(opts.InNamespace))
	}

	if err := b.cluster.GetClient().List(ctx, hwList, listOpts...); err != nil {
		err = fmt.Errorf("failed to list v1alpha2 hardware: %w", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Filter by MAC address in NetworkInterfaces map.
	if opts.ByMACAddress != "" {
		macKey := v1alpha2.MAC(opts.ByMACAddress)
		for i := range hwList.Items {
			if _, ok := hwList.Items[i].Spec.NetworkInterfaces[macKey]; ok {
				return &hwList.Items[i], nil
			}
		}
		err := hardwareNotFoundError{
			name:      fmt.Sprintf("v1alpha2 hardware with MAC %q", opts.ByMACAddress),
			namespace: func() string {
				if opts.InNamespace == "" {
					return "all namespaces"
				}
				return opts.InNamespace
			}(),
		}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Filter by IP address in IPAM.
	if opts.ByIPAddress != "" {
		for i := range hwList.Items {
			for _, ni := range hwList.Items[i].Spec.NetworkInterfaces {
				if ni.IPAM != nil {
					if ni.IPAM.IPv4 != nil && ni.IPAM.IPv4.Address == opts.ByIPAddress {
						return &hwList.Items[i], nil
					}
					if ni.IPAM.IPv6 != nil && ni.IPAM.IPv6.Address == opts.ByIPAddress {
						return &hwList.Items[i], nil
					}
				}
			}
		}
		err := hardwareNotFoundError{
			name:      fmt.Sprintf("v1alpha2 hardware with IP %q", opts.ByIPAddress),
			namespace: func() string {
				if opts.InNamespace == "" {
					return "all namespaces"
				}
				return opts.InNamespace
			}(),
		}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Filter by AgentID.
	if opts.ByAgentID != "" {
		for i := range hwList.Items {
			if hwList.Items[i].Spec.AgentID == opts.ByAgentID {
				return &hwList.Items[i], nil
			}
		}
		err := hardwareNotFoundError{
			name:      fmt.Sprintf("v1alpha2 hardware with agentID %q", opts.ByAgentID),
			namespace: func() string {
				if opts.InNamespace == "" {
					return "all namespaces"
				}
				return opts.InNamespace
			}(),
		}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if len(hwList.Items) == 0 {
		err := hardwareNotFoundError{name: "v1alpha2 hardware", namespace: "all namespaces"}
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// No specific filter applied, return first item (for POC).
	return &hwList.Items[0], nil
}
