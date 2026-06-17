package main

import (
	"bytes"
	"encoding/binary"
	"os"
)

// genIcon writes a 16x16 32-bit ICO file at path with a sky-blue→fuchsia
// gradient (matches the SMAGo banner colours).
func genIcon(path string) error {
	const w, h = 16, 16
	// Gradient: top-left sky-400 (56, 189, 248) → bottom-right fuchsia-500 (217, 70, 239)
	pixels := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			t := float64(x+y) / float64(w+h-2)
			r := uint8(56*(1-t) + 217*t)
			g := uint8(189*(1-t) + 70*t)
			b := uint8(248*(1-t) + 239*t)
			// BGRA, bottom-up — bottom row goes first
			pixels[i+0] = b
			pixels[i+1] = g
			pixels[i+2] = r
			pixels[i+3] = 0xFF
		}
	}
	// XOR mask (the actual image) is bottom-up rows. We need to flip vertically.
	flipped := make([]byte, len(pixels))
	for y := 0; y < h; y++ {
		copy(flipped[y*w*4:], pixels[(h-1-y)*w*4:(h-y)*w*4])
	}
	// AND mask: all 0 (fully opaque).
	andMask := make([]byte, w*h/8)

	var b bytes.Buffer
	// ICONDIR
	_ = binary.Write(&b, binary.LittleEndian, uint16(0))   // reserved
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))   // type = icon
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))   // count
	// ICONDIRENTRY
	b.WriteByte(byte(w))                                    // width
	b.WriteByte(byte(h))                                    // height
	b.WriteByte(0)                                          // palette
	b.WriteByte(0)                                          // reserved
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))   // planes
	_ = binary.Write(&b, binary.LittleEndian, uint16(32))  // bpp
	// size of image data = 40 (header) + flipped + andMask
	imgSize := uint32(40 + len(flipped) + len(andMask))
	_ = binary.Write(&b, binary.LittleEndian, imgSize)
	// offset of image data = 6 (ICONDIR) + 16 (entry)
	_ = binary.Write(&b, binary.LittleEndian, uint32(22))
	// BITMAPINFOHEADER
	_ = binary.Write(&b, binary.LittleEndian, uint32(40))
	_ = binary.Write(&b, binary.LittleEndian, int32(w))
	_ = binary.Write(&b, binary.LittleEndian, int32(h*2))    // biHeight = 2x for icons
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))    // planes
	_ = binary.Write(&b, binary.LittleEndian, uint16(32))   // bpp
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))    // compression
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(flipped)+len(andMask)))
	_ = binary.Write(&b, binary.LittleEndian, int32(0))     // x ppm
	_ = binary.Write(&b, binary.LittleEndian, int32(0))     // y ppm
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))    // clr used
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))    // clr important
	b.Write(flipped)
	b.Write(andMask)
	return os.WriteFile(path, b.Bytes(), 0644)
}

func main() {
	out := "smago.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := genIcon(out); err != nil {
		panic(err)
	}
}
