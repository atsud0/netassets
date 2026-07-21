package plugin

import "context"

type Manifest struct {
	ID          string
	Name        string
	Version     string
	Description string
	Permissions []string
}

type Plugin interface {
	Manifest() Manifest
	Start(context.Context) error
	Stop(context.Context) error
}

type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Plugin)}
}

func (r *Registry) Register(item Plugin) {
	r.plugins[item.Manifest().ID] = item
}

func (r *Registry) Get(id string) (Plugin, bool) {
	item, ok := r.plugins[id]
	return item, ok
}
