package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	DefaultMaxUploadBytes  int64 = 20 << 20 // 单张原始上传上限 20 MiB
	DefaultMaxDimension          = 1600     // 压缩后长边像素上限
	DefaultJPEGQuality           = 80       // 有损压缩质量
	maxDecodedDimension          = 12000    // 单边像素上限，防止解压炸弹
	maxDecodedPixels       int64 = 50_000_000
	maxAnimatedGIFBytes          = 2 << 20 // 小于 2 MiB 的合规动图保留动画
	MaxAttachmentsPerTurn        = 8
)

var (
	ErrUnsupportedFormat = errors.New("unsupported image format")
	ErrImageTooLarge     = errors.New("image exceeds maximum upload size")
	ErrImageDimensions   = errors.New("image dimensions exceed allowed limit")
	ErrEmptyImage        = errors.New("image payload is empty")
)

type ProcessedImage struct {
	Data      []byte
	MimeType  string
	Ext       string
	Width     int
	Height    int
	SizeBytes int64
	SHA256    string
}

// DetectImageFormat 仅依据文件头魔数识别允许的安全图片格式（JPEG / PNG / WebP / GIF），
// 绝不信任客户端传入的 Content-Type 或文件名后缀，并明确拒绝 SVG 等可执行格式。
func DetectImageFormat(data []byte) (mimeType string, ext string, ok bool) {
	if len(data) < 12 {
		return "", "", false
	}
	// 明确拒绝 SVG/XML/HTML 等文本或脚本载体，防止伪装。
	trimmed := bytes.TrimSpace(data[:min(len(data), 512)])
	lowerHead := strings.ToLower(string(trimmed))
	if strings.HasPrefix(lowerHead, "<svg") ||
		strings.HasPrefix(lowerHead, "<?xml") ||
		strings.HasPrefix(lowerHead, "<!doctype") ||
		strings.HasPrefix(lowerHead, "<html") {
		return "", "", false
	}

	switch {
	case data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg", ".jpg", true
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png", ".png", true
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", ".gif", true
	case bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", ".webp", true
	default:
		return "", "", false
	}
}

// ProcessImage 校验图片魔数与尺寸上限，执行 EXIF 方向修正、等比缩放与有损/无损压缩，
// 将常规数 MB 原图控制在百 KB 级，同时避免反向放大本已很小的 WebP/JPEG。
func ProcessImage(raw []byte, maxDimension int) (ProcessedImage, error) {
	if len(raw) == 0 {
		return ProcessedImage{}, ErrEmptyImage
	}
	if maxDimension <= 0 {
		maxDimension = DefaultMaxDimension
	}

	origMime, origExt, ok := DetectImageFormat(raw)
	if !ok {
		return ProcessedImage{}, fmt.Errorf("%w: only JPEG, PNG, WebP, and GIF images are allowed", ErrUnsupportedFormat)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return ProcessedImage{}, fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxDecodedDimension || cfg.Height > maxDecodedDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxDecodedPixels {
		return ProcessedImage{}, fmt.Errorf("%w: %dx%d", ErrImageDimensions, cfg.Width, cfg.Height)
	}

	// 小体积合规动图直接保留，避免破坏多帧动画体验。
	if origMime == "image/gif" {
		if g, err := gif.DecodeAll(bytes.NewReader(raw)); err == nil && len(g.Image) > 1 {
			if len(raw) <= maxAnimatedGIFBytes && cfg.Width <= maxDimension && cfg.Height <= maxDimension {
				return finalizeProcessed(raw, origMime, origExt, cfg.Width, cfg.Height), nil
			}
		}
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return ProcessedImage{}, fmt.Errorf("%w: decode failed: %v", ErrUnsupportedFormat, err)
	}

	transformed := false
	if origMime == "image/jpeg" {
		if orient := readJPEGExifOrientation(raw); orient > 1 && orient <= 8 {
			img = applyExifOrientation(img, orient)
			transformed = true
		}
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w > maxDimension || h > maxDimension {
		newW, newH := fitDimensions(w, h, maxDimension)
		dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))
		xdraw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)
		img = dst
		w, h = newW, newH
		transformed = true
	}

	hasAlpha := imageHasAlpha(img)
	var encoded bytes.Buffer
	var encMime, encExt string
	if hasAlpha {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&encoded, img); err != nil {
			return ProcessedImage{}, fmt.Errorf("encode png: %w", err)
		}
		encMime = "image/png"
		encExt = ".png"
	} else {
		if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: DefaultJPEGQuality}); err != nil {
			return ProcessedImage{}, fmt.Errorf("encode jpeg: %w", err)
		}
		encMime = "image/jpeg"
		encExt = ".jpg"
	}

	// 若未触发缩放/旋转且原图本身更小（例如前端已压好的 WebP 或小 PNG/JPEG），保留原字节避免体积膨胀。
	if !transformed && len(raw) < encoded.Len() {
		return finalizeProcessed(raw, origMime, origExt, w, h), nil
	}
	return finalizeProcessed(encoded.Bytes(), encMime, encExt, w, h), nil
}

func finalizeProcessed(data []byte, mimeType, ext string, width, height int) ProcessedImage {
	sum := sha256.Sum256(data)
	return ProcessedImage{
		Data:      data,
		MimeType:  mimeType,
		Ext:       ext,
		Width:     width,
		Height:    height,
		SizeBytes: int64(len(data)),
		SHA256:    hex.EncodeToString(sum[:]),
	}
}

func fitDimensions(w, h, maxEdge int) (int, int) {
	if w <= maxEdge && h <= maxEdge {
		return w, h
	}
	scale := float64(maxEdge) / float64(max(w, h))
	newW := max(1, int(math.Round(float64(w)*scale)))
	newH := max(1, int(math.Round(float64(h)*scale)))
	return newW, newH
}

func imageHasAlpha(img image.Image) bool {
	type opaquer interface {
		Opaque() bool
	}
	if op, ok := img.(opaquer); ok {
		return !op.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xFFFF {
				return true
			}
		}
	}
	return false
}

// readJPEGExifOrientation 解析 JPEG APP1/Exif 中的 Orientation 标签（1-8）。
// 仅解析必要字段，解析失败时安全返回 1（不旋转）。
func readJPEGExifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 1
		}
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			return 1
		}
		marker := data[pos]
		pos++
		if marker == 0xDA || marker == 0xD9 { // SOS / EOI
			return 1
		}
		if pos+2 > len(data) {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if segLen < 2 || pos+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 && segLen > 8 && bytes.Equal(data[pos+2:pos+8], []byte("Exif\x00\x00")) {
			return parseTIFFOrientation(data[pos+8 : pos+segLen])
		}
		pos += segLen
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		bo = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := int(bo.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 1
	}
	entries := int(bo.Uint16(tiff[ifdOffset : ifdOffset+2]))
	for i := 0; i < entries; i++ {
		off := ifdOffset + 2 + i*12
		if off+12 > len(tiff) {
			break
		}
		tag := bo.Uint16(tiff[off : off+2])
		if tag == 0x0112 { // Orientation
			val := int(bo.Uint16(tiff[off+8 : off+10]))
			if val >= 1 && val <= 8 {
				return val
			}
		}
	}
	return 1
}

func applyExifOrientation(img image.Image, orient int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.NRGBA
	if orient >= 5 && orient <= 8 {
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewNRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			switch orient {
			case 2:
				dst.Set(w-1-x, y, c)
			case 3:
				dst.Set(w-1-x, h-1-y, c)
			case 4:
				dst.Set(x, h-1-y, c)
			case 5:
				dst.Set(y, x, c)
			case 6:
				dst.Set(h-1-y, x, c)
			case 7:
				dst.Set(h-1-y, w-1-x, c)
			case 8:
				dst.Set(y, w-1-x, c)
			default:
				dst.Set(x, y, c)
			}
		}
	}
	return dst
}
