package imageinput

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

type orientedImage struct {
	source      image.Image
	orientation uint16
}

func (i orientedImage) ColorModel() color.Model { return i.source.ColorModel() }

func (i orientedImage) Bounds() image.Rectangle {
	b := i.source.Bounds()
	w, h := b.Dx(), b.Dy()
	if i.orientation >= 5 {
		w, h = h, w
	}
	return image.Rect(0, 0, w, h)
}

func (i orientedImage) At(x, y int) color.Color {
	b := i.source.Bounds()
	w, h := b.Dx(), b.Dy()
	switch i.orientation {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return i.source.At(b.Min.X+x, b.Min.Y+y)
}

func exifOrientation(data []byte, format string) uint16 {
	var exif []byte
	switch format {
	case formatJPEG:
		exif = jpegEXIF(data)
	case formatPNG:
		exif = chunkEXIF(data, 8, binary.BigEndian, false)
	case formatWebP:
		exif = chunkEXIF(data, 12, binary.LittleEndian, true)
	}
	exif = bytes.TrimPrefix(exif, []byte("Exif\x00\x00"))
	return tiffOrientation(exif)
}

func jpegEXIF(data []byte) []byte {
	for offset := 2; offset < len(data); {
		marker, segment, next := jpegSegment(data, offset)
		if next == 0 {
			return nil
		}
		if jpegEXIFSegment(marker, segment) {
			return segment
		}
		offset = next
	}
	return nil
}

// jpegSegment skips marker padding and standalone markers, and never traverses
// entropy-coded scan data. A zero next offset marks the end or corrupt input.
func jpegSegment(data []byte, offset int) (byte, []byte, int) {
	const prefix = 0xff
	if offset >= len(data) || data[offset] != prefix {
		return 0, nil, 0
	}
	for offset < len(data) && data[offset] == prefix {
		offset++
	}
	if offset >= len(data) || jpegTerminal(data[offset]) {
		return 0, nil, 0
	}
	marker := data[offset]
	offset++
	if marker == 1 || marker >= 0xd0 && marker <= 0xd8 {
		return marker, nil, offset
	}
	return jpegPayload(data, offset, marker)
}

func jpegPayload(data []byte, offset int, marker byte) (byte, []byte, int) {
	if offset+2 > len(data) {
		return 0, nil, 0
	}
	size := int(binary.BigEndian.Uint16(data[offset:]))
	if size < 2 || size > len(data)-offset {
		return 0, nil, 0
	}
	return marker, data[offset+2 : offset+size], offset + size
}

func jpegTerminal(marker byte) bool { return marker == 0xda || marker == 0xd9 }

func jpegEXIFSegment(marker byte, segment []byte) bool {
	return marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00"))
}

func chunkEXIF(data []byte, offset int64, order binary.ByteOrder, webp bool) []byte {
	for offset+8 <= int64(len(data)) {
		sizeOffset, typeOffset := offset, offset+4
		if webp {
			sizeOffset, typeOffset = offset+4, offset
		}
		size := int64(order.Uint32(data[sizeOffset:]))
		if size > int64(len(data))-offset-8 {
			return nil
		}
		end := offset + 8 + size
		kind := string(data[typeOffset : typeOffset+4])
		if kind == "eXIf" || kind == "EXIF" {
			return data[offset+8 : end]
		}
		offset = end + 4
		if webp {
			offset = end + size%2
		}
	}
	return nil
}

func tiffOrientation(data []byte) uint16 {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return 1
	}
	offset := int64(order.Uint32(data[4:]))
	if offset > int64(len(data))-2 {
		return 1
	}
	count := int64(order.Uint16(data[offset:]))
	start := offset + 2
	for index := range min(count, (int64(len(data))-start)/12) {
		entry := data[start+index*12:]
		if order.Uint16(entry) == 0x112 {
			return orientationValue(entry, order)
		}
	}
	return 1
}

func orientationValue(entry []byte, order binary.ByteOrder) uint16 {
	if order.Uint16(entry[2:]) != 3 || order.Uint32(entry[4:]) != 1 {
		return 1
	}
	value := order.Uint16(entry[8:])
	if value < 1 || value > 8 {
		return 1
	}
	return value
}
