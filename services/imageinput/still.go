package imageinput

import (
	"bytes"
	"encoding/binary"
)

// The comparison decoder preserves the classifier's still-image restriction
// without loading native pixels or accepting the first frame of an animation.
func multipleImages(data []byte, format string) bool {
	switch format {
	case formatJPEG:
		return jpegMPF(data)
	case formatPNG:
		return pngAnimation(data)
	case formatWebP:
		return len(data) >= 21 && string(data[12:16]) == "VP8X" && data[20]&2 != 0
	case formatAVIF:
		return avifSequence(data)
	default:
		return false
	}
}

func jpegMPF(data []byte) bool {
	for offset := 2; offset < len(data); {
		marker, segment, next := jpegSegment(data, offset)
		if next == 0 {
			return false
		}
		if marker == 0xe2 && bytes.HasPrefix(segment, []byte("MPF\x00")) {
			return true
		}
		offset = next
	}
	return false
}

func pngAnimation(data []byte) bool {
	for offset := 8; offset+12 <= len(data); {
		size := int64(binary.BigEndian.Uint32(data[offset:]))
		if size > int64(len(data)-offset-12) {
			return false
		}
		if string(data[offset+4:offset+8]) == "acTL" {
			return true
		}
		offset += int(size) + 12
	}
	return false
}

func avifSequence(data []byte) bool {
	if len(data) < 16 {
		return false
	}
	size := int64(binary.BigEndian.Uint32(data))
	if size < 16 || size > int64(len(data)) {
		return false
	}
	if string(data[8:12]) == "avis" {
		return true
	}
	for offset := int64(16); offset+4 <= size; offset += 4 {
		if string(data[offset:offset+4]) == "avis" {
			return true
		}
	}
	return false
}
