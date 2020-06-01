package main

type SourceLoader interface {
	Load(url string) ([]byte, error)
}

type LoaderNewFunc func(args map[string]string) SourceLoader

var loaders map[string]LoaderNewFunc

func RegisterLoader(name string, newfunc LoaderNewFunc) {
	if loaders == nil {
		loaders = make(map[string]LoaderNewFunc)
	}
	loaders[name] = newfunc
}
