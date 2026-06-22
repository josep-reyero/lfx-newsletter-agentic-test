// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain

import "errors"

// Domain-level sentinel errors. Repository, service, and handler layers wrap and
// translate these via errors.Is to keep transport-specific knowledge out of the
// domain layer.
var (
	// ErrNotFound indicates a record with the requested identifier does not exist.
	ErrNotFound = errors.New("not found")

	// ErrVersionMismatch indicates an optimistic-locking conflict: the caller's
	// expected version does not match the row's current version.
	ErrVersionMismatch = errors.New("version mismatch")

	// ErrInvalidRequest indicates the caller's input failed validation.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrAlreadySent indicates a draft has already been sent and cannot be re-sent
	// or modified.
	ErrAlreadySent = errors.New("newsletter already sent")

	// ErrSendInProgress indicates a draft has been claimed by the send
	// orchestrator but has not yet finalized as sent. It cannot be edited or
	// deleted without risking duplicate or mismatched deliveries.
	ErrSendInProgress = errors.New("newsletter send in progress")

	// ErrForbidden indicates the caller is authorized for the request's project
	// but referenced a resource (e.g. a committee) that belongs to a different
	// project and is therefore out of scope.
	ErrForbidden = errors.New("forbidden")
)
