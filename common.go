package main

type SourceLoader interface {
	Load(url string) ([]byte, error)
}

type Storage interface {
	Save(key string, value []byte) error
	Load(key string) ([]byte, error)
}

type LoaderNewFunc func(args map[string]string) SourceLoader

var loaders map[string]LoaderNewFunc

func RegisterLoader(name string, newfunc LoaderNewFunc) {
	if loaders == nil {
		loaders = make(map[string]LoaderNewFunc)
	}
	loaders[name] = newfunc
}

type StorageNewFunc func(args map[string]string) Storage

var storages map[string]StorageNewFunc

func RegisterStorage(name string, newfunc StorageNewFunc) {
	if storages == nil {
		storages = make(map[string]StorageNewFunc)
	}
	storages[name] = newfunc
}
