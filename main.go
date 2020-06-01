package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/golang/groupcache"
	"gopkg.in/yaml.v3"
)

var (
	configName string
)

type StatusStruct struct {
	RequestsReceived uint64
	Inflight         uint64
	ThumbsGenerated  uint64
	ThumbsFailed     uint64
}

type StatusResponse struct {
	Thumbs    StatusStruct
	HotCache  groupcache.CacheStats
	MainCache groupcache.CacheStats
}

var status StatusStruct

func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hdr := w.Header()

	hdr.Set("Content-Type", "application/json")
	var resp StatusResponse
	resp.Thumbs = status
	resp.MainCache = Group.CacheStats(groupcache.MainCache)
	resp.HotCache = Group.CacheStats(groupcache.HotCache)
	stjson, _ := json.MarshalIndent(resp, "", "  ")
	w.Write(stjson)
}

func thumbHandler(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&status.RequestsReceived, 1)
	atomic.AddUint64(&status.Inflight, 1)
	defer atomic.AddUint64(&status.Inflight, 0xffffffffffffffff)
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uri := strings.SplitN(r.RequestURI, "/", 3)
	if len(uri) != 3 || uri[0] != "" {
		http.Error(w, "Malformed URI", 500)
		return
	}

	var data []byte
	err := Group.Get(r.Context(), uri[2], groupcache.AllocatingByteSliceSink(&data))
	if err != nil {
		log.Printf("GC: %s", err.Error())
		if v, ok := err.(*HTTPLoaderError); ok {
			if v2, ok := v.What.(*url.Error); ok {
				if v3, ok := v2.Err.(*net.OpError); ok {
					log.Printf("GCue: %#v", v3)
				}
			}
		}
		http.Error(w, "Unable to retrieve", 404)
		return
	}
	var thumb Thumb
	if _, err = thumb.Unmarshal(data); err != nil {
		log.Printf("Unmarshal: %s", err)
		http.Error(w, "Internal error", 500)
		return
	}
	w.Header().Set("Content-Type", thumb.Mime)
	if Config.ResponseAge > 0 {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", Config.ResponseAge))
	}
	http.ServeContent(w, r, "", thumb.Created, bytes.NewReader(thumb.Data))
}

type Configuration struct {
	Size        int64             `yaml:"size"`
	Listen      string            `yaml:"listen"`
	ListenGC    string            `yaml:"listen_gc"`
	Me          string            `yaml:"me"`
	Peers       []string          `yaml:"peers"`
	Loader      string            `yaml:"loader"`
	LoaderArgs  map[string]string `yaml:"loader_args"`
	MaxWidth    int               `yaml:"max_width"`
	MaxHeight   int               `yaml:"max_height"`
	ResponseAge int               `yaml:"response_age"`
	MaxInDim    int               `yaml:"max_in_dim"`
	JpegQuality int               `yaml:"jpeg_quality"`
}

var (
	Config Configuration
	Group  *groupcache.Group
)

type Handler struct {
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/status" {
		statusHandler(w, r)
		return
	}
	thumbHandler(w, r)
}

func main() {
	flag.StringVar(&configName, "config", "", "configuration file name")

	flag.Parse()
	Config.Size = 128
	Config.Listen = "127.0.0.1:8080"
	Config.ListenGC = "127.0.0.1:8081"
	Config.Me = "http://127.0.0.1:8081"
	Config.Peers = []string{"http://127.0.0.1:8081"}
	Config.Loader = "http"
	Config.LoaderArgs = map[string]string{
		"timeout":  "10",
		"max_size": "20971520",
	}
	Config.MaxWidth = 512
	Config.MaxHeight = 512
	Config.MaxInDim = 1024
	Config.JpegQuality = 80
	if configName != "" {
		log.Printf("Loading configuration from %s", configName)
		file, err := os.Open(configName)
		if err != nil {
			log.Fatal(err)
		}
		decoder := yaml.NewDecoder(file)
		decoder.KnownFields(true)
		if err = decoder.Decode(&Config); err != nil {
			log.Fatal(err)
		}
		file.Close()
	}
	var envConfig string
	envPrefix := "BNW_THUMB_"
	for _, envItem := range os.Environ() {
		envPair := strings.SplitN(envItem, "=", 2)
		if len(envPair) == 2 && strings.HasPrefix(envPair[0], envPrefix) {
			envConfig += strings.ToLower(envPair[0][len(envPrefix):]) + ": " + envPair[1] + "\n"
		}
	}
	if envConfig != "" {
		log.Printf("Configuration synthesized from %s* environment variables:\n%s", envPrefix, envConfig)
		decoder := yaml.NewDecoder(strings.NewReader(envConfig))
		decoder.KnownFields(true)
		if err := decoder.Decode(&Config); err != nil {
			log.Fatal(err)
		}
	}
	cfgStr, err := yaml.Marshal(&Config)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Active configuration:\n%s", cfgStr)

	loader, ok := loaders[Config.Loader]
	if !ok {
		log.Fatalf("Loader %q doesn't exist.", Config.Loader)
	}

	renderer := &Renderer{
		Loader:      loader(Config.LoaderArgs),
		MaxWidth:    Config.MaxWidth,
		MaxHeight:   Config.MaxHeight,
		MaxInDim:    Config.MaxInDim,
		JpegQuality: Config.JpegQuality,
	}

	if Config.ListenGC == "" {
		if strings.HasPrefix(Config.Me, "http://") {
			Config.ListenGC = Config.Me[7:len(Config.Me)]
		}
	}
	peers := groupcache.NewHTTPPool(Config.Me)
	if len(Config.Peers) > 0 {
		peers.Set(Config.Peers...)
		go http.ListenAndServe(Config.ListenGC, http.HandlerFunc(peers.ServeHTTP))
	}
	Group = groupcache.NewGroup("bnw-thumb", Config.Size<<20, renderer)

	//http.HandleFunc("/status", statusHandler)
	//http.HandleFunc("/", thumbHandler)
	log.Printf("Going to listen on %s", Config.Listen)
	server := &http.Server{Addr: Config.Listen, Handler: Handler{}}
	server.ListenAndServe()
}
