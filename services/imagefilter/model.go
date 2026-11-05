package imagefilter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/caitun/inferkit/services/imagepipeline"
)

type node struct {
	Name    string  `json:"name"`
	DType   string  `json:"dtype"`
	Layout  string  `json:"layout"`
	Kind    string  `json:"kind"`
	Shape   []int64 `json:"shape"`
	Softmax bool    `json:"softmax_already_applied"`
}

type manifest struct {
	Training      *preprocessing `json:"training_preprocessing"`
	Model         string         `json:"model"`
	SHA256        string         `json:"sha256"`
	Precision     string         `json:"precision"`
	Labels        []string       `json:"labels"`
	Input         node           `json:"input"`
	Output        node           `json:"output"`
	Preprocessing preprocessing  `json:"preprocessing"`
	Dynamic       bool           `json:"dynamic"`
	Embedded      bool           `json:"preprocessing_embedded"`
}

type preprocessing struct {
	Profile      string           `json:"profile"`
	Backend      string           `json:"backend"`
	Version      string           `json:"libvips_version"`
	PyVersion    string           `json:"pyvips_version"`
	Reference    string           `json:"reference"`
	Operation    string           `json:"operation"`
	Kernel       string           `json:"kernel"`
	KernelSource string           `json:"kernel_source"`
	GapMode      string           `json:"gap_mode"`
	Shrink       string           `json:"shrink_on_load"`
	Color        string           `json:"color"`
	Resize       string           `json:"resize"`
	Resampling   string           `json:"resampling"`
	Rounding     string           `json:"rounding"`
	Centering    string           `json:"centering"`
	Alpha        string           `json:"alpha"`
	PixelScale   string           `json:"pixel_scale"`
	Thumbnail    thumbnailOptions `json:"thumbnail_options"`
	Canvas       [2]int           `json:"canvas"`
	Background   [3]int           `json:"background"`
	Mean         [3]int           `json:"mean"`
	Std          [3]int           `json:"std"`
	EXIF         bool             `json:"exif_transpose"`
	ICC          bool             `json:"icc_transform"`
	Linear       bool             `json:"linear_light"`
}

type thumbnailOptions struct {
	Size     string `json:"size"`
	Crop     string `json:"crop"`
	FailOn   string `json:"fail_on"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	NoRotate bool   `json:"no_rotate"`
	Linear   bool   `json:"linear"`
}

func loadModel(dir string) (*manifest, []byte, error) {
	m := new(manifest)
	if err := readJSON(filepath.Join(dir, "deployment.json"), m); err != nil {
		return nil, nil, err
	}
	var labels []string
	if err := readJSON(filepath.Join(dir, "labels.json"), &labels); err != nil {
		return nil, nil, err
	}
	if err := m.validate(labels); err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, m.Model)) //nolint:gosec // Trusted operator directory; manifest model is validated as a single local basename.
	if err != nil {
		return nil, nil, errors.Join(ErrModel, err)
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != m.SHA256 {
		return nil, nil, ErrModel
	}
	return m, data, nil
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path) //nolint:gosec // Only operator-configured deployment metadata and validation report paths reach this helper.
	if err != nil {
		return errors.Join(ErrModel, err)
	}
	if err = json.Unmarshal(data, dst); err != nil {
		return errors.Join(ErrModel, err)
	}
	return nil
}

func (m *manifest) validate(labels []string) error {
	if !filepath.IsLocal(m.Model) || filepath.Base(m.Model) != m.Model || m.Dynamic || m.Embedded || m.Precision != "FP32" {
		return ErrModel
	}
	if m.Training != nil && *m.Training != m.Preprocessing {
		return ErrModel
	}
	return errors.Join(validateLabels(labels, m.Labels), m.Input.validateInput(), m.Output.validateOutput(), m.Preprocessing.validate())
}

func validateLabels(labels, expected []string) error {
	if len(labels) != 2 || !slices.Equal(labels, expected) || labels[0] == "" || labels[1] == "" || labels[0] == labels[1] {
		return ErrModel
	}
	return nil
}

func (n node) validateInput() error {
	if n.Name == "" || n.DType != "float32" || n.Layout != "NCHW" || !slices.Equal(n.Shape, []int64{1, channels, imageSize, imageSize}) {
		return ErrModel
	}
	return nil
}

func (n node) validateOutput() error {
	if n.Name == "" || n.DType != "float32" || n.Kind != "probabilities" || !n.Softmax {
		return ErrModel
	}
	if !slices.Equal(n.Shape, []int64{1, 2}) {
		return ErrModel
	}
	return nil
}

func (p preprocessing) validate() error {
	expected := expectedPreprocessing()
	// Provenance does not change the operation contract. Log the actual library
	// version at startup and validate exported tensors on each deployment.
	expected.Version, expected.PyVersion, expected.Reference = p.Version, p.PyVersion, p.Reference
	if p != expected || p.Version == "" {
		return ErrModel
	}
	return nil
}

func expectedPreprocessing() preprocessing {
	return preprocessing{
		Profile: imagepipeline.Profile, Backend: "libvips", EXIF: true, Color: "sRGB",
		Operation: "thumbnail_buffer", Kernel: "lanczos3", KernelSource: "libvips_thumbnail_internal_default",
		GapMode: "libvips_thumbnail_internal_default", Shrink: "auto_by_loader",
		Rounding: "libvips_thumbnail_native", Centering: "floor_divide_by_2",
		Alpha:      "thumbnail_internal_premultiply_unpremultiply_then_cast_uchar_flatten",
		PixelScale: "float32(pixel) / float32(255)",
		Thumbnail:  thumbnailOptions{Width: imageSize, Height: imageSize, Size: "both", Crop: "none", FailOn: "error"},
		Canvas:     [2]int{imageSize, imageSize}, Background: [3]int{pixelMax, pixelMax, pixelMax}, Std: [3]int{1, 1, 1},
	}
}
