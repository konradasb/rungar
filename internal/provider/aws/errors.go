// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

// refusal is what EC2's refusal of a request says: of a request creating an
// instance, what it says of the subnet.
type refusal int

const (
	// refusalUncertain is EC2 not answering, or failing as it answered: the
	// outcome is unknown, and the instance may have been created.
	refusalUncertain refusal = iota

	// refusalOther is EC2 refusing for a reason of the account's, such as
	// a missing permission or too many requests, which every subnet
	// shares. No instance was created.
	refusalOther

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

	// refusalInvalid is an instance that can never be created: an AMI or
	// security group that does not exist, a malformed field.
	refusalInvalid
)

// refusalOf returns what an error from EC2 says. Of any request it may be
// refusalUncertain, refusalOther or refusalInvalid; the others are of a
// request creating an instance, and say what it says of the subnet.
func refusalOf(err error) refusal {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return refusalUncertain
	}

	switch code := apiErr.ErrorCode(); {
	case code == "InternalError",
		code == "InternalFailure",
		code == "ServiceUnavailable",
		code == "Unavailable":
		return refusalUncertain
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

// shortError is an error from EC2 that reads as errorMessage has it, without
// the request that failed, and still unwraps to it.
type shortError struct {
	err error
}

func (e shortError) Error() string { return errorMessage(e.err) }

func (e shortError) Unwrap() error { return e.err }
