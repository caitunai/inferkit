package imagefilter

import (
	"errors"
	"testing"
)

func TestNativeModelProfileRejectsDifferentOperations(t *testing.T) {
	t.Parallel()
	valid := expectedPreprocessing()
	valid.Version = "8.18.7"
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*preprocessing){
		func(p *preprocessing) { p.Profile = "pillow" },
		func(p *preprocessing) { p.ICC = true },
		func(p *preprocessing) { p.Linear = true },
		func(p *preprocessing) { p.EXIF = false },
		func(p *preprocessing) { p.Thumbnail.Crop = "centre" },
		func(p *preprocessing) { p.Thumbnail.Size = "force" },
		func(p *preprocessing) { p.Thumbnail.NoRotate = true },
		func(p *preprocessing) { p.Shrink = "disabled" },
		func(p *preprocessing) { p.Resampling = "Pillow RGBA LANCZOS" },
	} {
		changed := valid
		mutate(&changed)
		if err := changed.validate(); !errors.Is(err, ErrModel) {
			t.Fatalf("incompatible preprocessing accepted: %+v", changed)
		}
	}
}
