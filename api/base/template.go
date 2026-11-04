package base

import (
	"errors"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin/render"
	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/storage"
)

// Renderer represents renderer data.
type Renderer interface {
	render.HTMLRender
	LoadTemplates()
}

// Render type
type Render map[string]*template.Template

var (
	_ render.HTMLRender = Render{}
	_ Renderer          = Render{}
	// ErrReadLayout indicates error to read layout file.
	ErrReadLayout = errors.New("error to read layout file")
	// ErrPageTemplate indicates error to read page template.
	ErrPageTemplate = errors.New("error to read page template")
)

// NewRender instance
func NewRender() Render {
	r := make(Render)
	r.LoadTemplates()
	return r
}

// Instance supply render string
func (r Render) Instance(name string, data any) render.Render {
	return render.HTML{
		Template: r[name],
		Name:     name,
		Data:     data,
	}
}

// LoadTemplates loads templates.
func (r Render) LoadTemplates() {
	includes, err := fs.Glob(storage.FS, "views/**/*.html")
	if err != nil {
		log.Error().Err(errors.Join(ErrReadLayout, err)).Msg("Find html templates failed")
		return
	}
	tpl := r.loadLayouts(includes)
	for _, include := range includes {
		if r.isLayout(include) || r.isShared(include) {
			continue
		}
		r.loadPage(tpl, include)
	}
}

func (r Render) loadLayouts(includes []string) *template.Template {
	tpl := template.New("tpl")
	for _, include := range includes {
		if !r.isLayout(include) && !r.isShared(include) {
			continue
		}
		content, err := r.readLayoutFile(include)
		if err != nil {
			log.Error().Err(err).Str("template", include).Msg("Read html template failed")
			continue
		}
		if _, parseErr := tpl.Parse(string(content)); parseErr != nil {
			log.Error().Err(parseErr).Str("template", include).Msg("Parse html template failed")
		}
	}
	return tpl
}

func (r Render) loadPage(baseTemplate *template.Template, include string) {
	pageName := templatePageName(include)
	clone, err := baseTemplate.Clone()
	if err != nil {
		log.Error().Err(err).Str("page", pageName).Msg("Clone html template failed")
		return
	}
	tmpl := clone.New(pageName)
	content, err := r.readPageTemplateFile(include, pageName)
	if err != nil {
		log.Error().Err(err).Str("page", pageName).Msg("Read html page template failed")
		return
	}
	if _, parseErr := tmpl.Parse(string(content)); parseErr != nil {
		log.Error().Err(parseErr).Str("page", pageName).Msg("Parse html page template failed")
		return
	}
	r[pageName] = tmpl
}

func templatePageName(include string) string {
	pageName := strings.TrimSuffix(include, filepath.Ext(include))
	pageName = strings.TrimSuffix(pageName, filepath.Ext(pageName))
	pageName = strings.TrimPrefix(pageName, "views/")
	return strings.ReplaceAll(pageName, "/", ".")
}

func (r Render) isLayout(file string) bool {
	return strings.Contains(file, "/layout/")
}

func (r Render) isShared(file string) bool {
	return strings.Contains(file, "/shared/")
}

func (r Render) getFileName(fileName string) string {
	return strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
}

func (r Render) getTemplateName(fileName string) string {
	if strings.Contains(fileName, ".") {
		return strings.Trim(filepath.Ext(fileName), ".")
	}
	return ""
}

func (r Render) readLayoutFile(file string) ([]byte, error) {
	content, err := storage.FS.ReadFile(file)
	if err == nil {
		tplName := r.getFileName(file)
		prefix := []byte("{{ define \"" + tplName + "\" }}")
		suffix := []byte("{{ end }}")
		content = append(prefix, content...)
		content = append(content, suffix...)
	} else {
		err = errors.Join(err, ErrReadLayout)
	}
	return content, err
}

func (r Render) readPageTemplateFile(file, pageName string) ([]byte, error) {
	content, err := storage.FS.ReadFile(file)
	if err == nil {
		tplName := r.getFileName(file)
		tmpName := r.getTemplateName(tplName)
		if tmpName == "" {
			prefix := []byte("{{ define \"" + pageName + "\" }}")
			suffix := []byte("{{ end }}")
			content = append(prefix, content...)
			content = append(content, suffix...)
			return content, nil
		}
		prefix := []byte("{{ define \"" + pageName + "\" }}{{ template \"" + tmpName + "\" .}}{{ end }}")
		content = append(prefix, content...)
	} else {
		err = errors.Join(err, ErrPageTemplate)
	}
	return content, err
}
