package layout

import "path/filepath"

type Layout struct {
	root string
}

func New(root string) Layout {
	return Layout{root: root}
}

func (l Layout) SandboxDir(sandboxID string) string {
	return filepath.Join(l.root, "sandboxes", sandboxID)
}

func (l Layout) DBPath() string {
	return filepath.Join(l.root, "novitabox.db")
}

func (l Layout) ImageDir(imageID string) string {
	return filepath.Join(l.root, "images", imageID)
}

func (l Layout) TemplateDir(templateID string) string {
	return filepath.Join(l.root, "templates", templateID)
}
