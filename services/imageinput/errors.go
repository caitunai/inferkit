package imageinput

import "errors"

var (
	// ErrInput identifies an unreadable or unsupported image.
	ErrInput = errors.New("invalid image input")
	// ErrTooLarge identifies an encoded size or decoded pixel limit violation.
	ErrTooLarge = errors.New("image exceeds configured limit")
	// ErrURL identifies an unsupported image URL.
	ErrURL = errors.New("invalid image URL")
	// ErrDNS identifies failed DNS resolution.
	ErrDNS = errors.New("image host DNS resolution failed")
	// ErrDownload identifies a failed image download.
	ErrDownload = errors.New("image download failed")
)
