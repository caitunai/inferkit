package imageinput

import (
	"errors"
	"net"
	"strconv"
)

func classifyDownloadError(err error) error {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return errors.Join(ErrDownload, ErrDNS, err)
	}
	return errors.Join(ErrDownload, err)
}

// DownloadStatusError describes an unsuccessful response without storing its body or URL.
type DownloadStatusError struct {
	Host        string
	ContentType string
	StatusCode  int
}

func (e *DownloadStatusError) Error() string {
	return ErrDownload.Error() + ": HTTP " + strconv.Itoa(e.StatusCode)
}

// Unwrap allows existing download error handling to keep matching the sentinel.
func (*DownloadStatusError) Unwrap() error { return ErrDownload }
