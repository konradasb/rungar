// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

// refusal is what EC2's refusal of an instance says of the subnet.
type refusal int

const (
	// refusalOther is EC2 failing.
	refusalOther refusal = iota

	// refusalFull is the subnet's Availability Zone out of capacity for the
	// instance type, or the subnet out of addresses: the next subnet may
	// have room.
	refusalFull

	// refusalQuota is the account out of a quota of the region's, which
	// every subnet shares.
	refusalQuota

	// refusalUnsupported is an instance type the subnet's zone does not
	// offer, which another zone may.
	refusalUnsupported

	// refusalInvalid is an instance that can never be made: an AMI or
	// security group that does not exist, a malformed field.
	refusalInvalid
)

// classify returns what an error making an instance says of the subnet.
func classify(err error) refusal {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return refusalOther
	}

	switch code := apiErr.ErrorCode(); {
	case code == "InsufficientInstanceCapacity",
		code == "InsufficientHostCapacity",
		code == "InsufficientCapacity",
		code == "InsufficientFreeAddressesInSubnet",
		code == "SpotMaxPriceTooLow":
		return refusalFull
	case code == "InstanceLimitExceeded",
		code == "VcpuLimitExceeded",
		code == "MaxSpotInstanceCountExceeded":
		return refusalQuota
	case code == "Unsupported":
		return refusalUnsupported
	case strings.HasPrefix(code, "Invalid"),
		code == "MissingParameter",
		code == "UnknownParameter":
		return refusalInvalid
	default:
		return refusalOther
	}
}

// isNotFound reports whether err is EC2 saying an instance is not there.
func isNotFound(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceID.NotFound"
}

// errorMessage returns the message of an error from EC2, without the request
// that failed.
func errorMessage(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorMessage() != "" {
		return apiErr.ErrorCode() + ": " + apiErr.ErrorMessage()
	}

	return err.Error()
}
