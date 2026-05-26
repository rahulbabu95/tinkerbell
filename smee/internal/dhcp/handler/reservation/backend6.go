package reservation

import (
	"context"

	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
)

// BackendReaderV2 looks up v1alpha2 Hardware objects.
type BackendReaderV2 interface {
	FilterHardwareV2(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error)
}
