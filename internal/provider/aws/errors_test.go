// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

func TestRefusalOfClassifiesEC2Errors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want refusal
	}{
		{"no reply", errors.New("read tcp: connection reset by peer"), refusalUncertain},
		{"deadline", context.DeadlineExceeded, refusalUncertain},
		{"InternalError", apiError("InternalError"), refusalUncertain},
		{"InternalFailure", apiError("InternalFailure"), refusalUncertain},
		{"ServiceUnavailable", apiError("ServiceUnavailable"), refusalUncertain},
		{"Unavailable", apiError("Unavailable"), refusalUncertain},
		{"InsufficientInstanceCapacity", apiError("InsufficientInstanceCapacity"), refusalFull},
		{"InsufficientHostCapacity", apiError("InsufficientHostCapacity"), refusalFull},
		{"InsufficientCapacity", apiError("InsufficientCapacity"), refusalFull},
		{"InsufficientFreeAddressesInSubnet", apiError("InsufficientFreeAddressesInSubnet"), refusalFull},
		{"SpotMaxPriceTooLow", apiError("SpotMaxPriceTooLow"), refusalFull},
		{"InstanceLimitExceeded", apiError("InstanceLimitExceeded"), refusalQuota},
		{"VcpuLimitExceeded", apiError("VcpuLimitExceeded"), refusalQuota},
		{"MaxSpotInstanceCountExceeded", apiError("MaxSpotInstanceCountExceeded"), refusalQuota},
		{"Unsupported", apiError("Unsupported"), refusalUnsupported},
		{"InvalidAMIID.NotFound", apiError("InvalidAMIID.NotFound"), refusalInvalid},
		{"InvalidParameterValue", apiError("InvalidParameterValue"), refusalInvalid},
		{"MissingParameter", apiError("MissingParameter"), refusalInvalid},
		{"UnknownParameter", apiError("UnknownParameter"), refusalInvalid},
		{"UnauthorizedOperation", apiError("UnauthorizedOperation"), refusalOther},
		{"RequestLimitExceeded", apiError("RequestLimitExceeded"), refusalOther},
		{"wrapped", fmt.Errorf("subnet-a: %w", apiError("VcpuLimitExceeded")), refusalQuota},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refusalOf(tt.err); got != tt.want {
				t.Errorf("refusalOf(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// apiError returns an error of EC2's with this code.
func apiError(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: "EC2 said no"}
}
