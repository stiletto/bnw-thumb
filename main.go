package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/groupcache"
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

	var ctx groupcache.Context
	var data []byte
	err := Group.Get(ctx, uri[2], groupcache.AllocatingByteSliceSink(&data))
	if err != nil {
		log.Printf("GC: %s", err.Error())
		if v, ok := err.(*HttpLoaderError); ok {
			if v2, ok := v.What.(*url.Error); ok {
				if v3, ok := v2.Err.(*net.OpError); ok {
					log.Printf("GCue: %#v", v3)
				}
			}
		}
		http.Error(w, "Unable to retreive", 404)
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

func init() {
	flag.StringVar(&configName, "config", "config.json", "configuration file name")
}

type Configuration struct {
	Size        int64             `json:"size"`
	Listen      string            `json:"listen"`
	ListenGC    string            `json:"listen_gc"`
	Me          string            `json:"me"`
	Peers       []string          `json:"peers"`
	Loader      string            `json:"loader"`
	LoaderArgs  map[string]string `json:"loader_args"`
	MaxWidth    int               `json:"max_width"`
	MaxHeight   int               `json:"max_height"`
	ResponseAge int               `json:"response_age"`
	MaxInDim    int               `json:"max_in_dim"`
	JpegQuality int               `json:"jpeg_quality"`
}

type InflightMap struct {
	data map[*http.Request]time.Time
	mu   sync.Mutex
}

func (im *InflightMap) Put(req *http.Request) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.data[req] = time.Now()
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

	flag.Parse()
	file, err := os.Open(configName)
	if err != nil {
		log.Fatal(err)
	}
	fileContents, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(fileContents, &Config)
	if err != nil {
		log.Fatal(err)
	}

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
	fmt.Fprintf(os.Stderr, "Going to listen on %s\n", Config.Listen)
	server := &http.Server{Addr: Config.Listen, Handler: Handler{}}
	server.ListenAndServe()
}
