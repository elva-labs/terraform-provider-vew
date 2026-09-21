package client

import (
	"errors"
	"fmt"
	"strings"
)

// CreateComponentInput is the mutable configuration required to create a component.
type CreateComponentInput struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Platform               string   `json:"platform"`
	SupportedArchitectures []string `json:"supportedArchitectures"`
	SupportedOSVersions    []string `json:"supportedOsVersions"`
}

// UpdateComponentInput contains the one configuration property VEW permits changing.
type UpdateComponentInput struct {
	Description string `json:"componentDescription"`
}

// Component is VEW's component representation.
type Component struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Platform               string   `json:"platform"`
	SupportedArchitectures []string `json:"supportedArchitectures"`
	SupportedOSVersions    []string `json:"supportedOsVersions"`
	Status                 string   `json:"status"`
	CreatedAt              string   `json:"createdAt"`
	CreatedBy              string   `json:"createdBy"`
	UpdatedAt              string   `json:"updatedAt"`
	UpdatedBy              string   `json:"updatedBy"`
}

// Problem is an RFC 9457-style error returned by VEW.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
	Retryable bool   `json:"retryable"`
}

// APIError reports a VEW problem without including credentials or response bodies.
type APIError struct {
	Status  int
	Problem Problem
}

func (e *APIError) Error() string {
	parts := []string{fmt.Sprintf("VEW API returned status %d", e.Status)}
	if detail := strings.TrimSpace(e.Problem.Detail); detail != "" {
		parts = append(parts, detail)
	} else if title := strings.TrimSpace(e.Problem.Title); title != "" {
		parts = append(parts, title)
	}
	if requestID := strings.TrimSpace(e.Problem.RequestID); requestID != "" {
		parts = append(parts, "request ID "+requestID)
	}
	return strings.Join(parts, ": ")
}

func fmtAPIError(status int, problem Problem) *APIError {
	if problem.Status == 0 {
		problem.Status = status
	}
	return &APIError{Status: status, Problem: problem}
}

// IsNotFound reports whether err is a VEW 404 response.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == 404
}
