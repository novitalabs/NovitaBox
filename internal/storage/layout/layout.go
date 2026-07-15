package layout

import (
	"path/filepath"

	"github.com/novitalabs/NovitaBox/internal/config"
)

type Layout struct {
	root string
}

func New(root string) Layout {
	return Layout{root: root}
}

func (l Layout) SandboxDir(sandboxID string) string {
	return filepath.Join(l.root, "sandboxes", sandboxID)
}

func (l Layout) SandboxJailerDir(sandboxID string) string {
	return filepath.Join(l.SandboxDir(sandboxID), "jails")
}

func (l Layout) DBPath() string {
	return config.DefaultDBPath(l.root)
}

func (l Layout) ImageDir(imageID string) string {
	return filepath.Join(l.root, "images", imageID)
}

func (l Layout) TemplateDir(templateID string) string {
	return filepath.Join(l.root, "templates", templateID)
}
