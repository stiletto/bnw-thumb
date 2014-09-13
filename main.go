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
    "sync/atomic"
    "time"
    "github.com/golang/groupcache"
)

var (
    configName string
)

type StatusStruct struct {
    RequestsProcessed uint64
    ThumbsGenerated uint64
    ThumbsFailed uint64
}

var status StatusStruct

func statusHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != "GET" {
        http.Error(w,"Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    hdr := w.Header()

    hdr.Set("Content-Type", "application/json")
    stjson, _ := json.MarshalIndent(status,"", "  ")
    w.Write(stjson)
}

func thumbHandler(w http.ResponseWriter, r *http.Request) {
    atomic.AddUint64(&status.RequestsProcessed, 1)
    if r.Method != "GET" {
        http.Error(w,"Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    uri := strings.SplitN(r.RequestURI, "/",3)
    if len(uri) != 3 || uri[0] != "" {
        http.Error(w,"Malformed URI", 500)
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
        http.Error(w,"Unable to retreive", 404)
        return
    }
    var modTime time.Time
    http.ServeContent(w, r, "big-file-thumb.jpg", modTime, bytes.NewReader(data))
}

func init() {
    flag.StringVar(&configName, "config", "config.json", "configuration file name")
}

type Configuration struct {
    Listen string `json:"listen"`
    ListenGC string `json:"listen_gc"`
    Me string `json:"me"`
    Peers []string `json:"peers"`
    Loader string `json:"loader"`
    LoaderArgs map[string]string `json:"loader_args"`
    MaxWidth int `json:"max_width"`
    MaxHeight int `json:"max_height"`
    MaxInDim int `json:"max_in_dim"`
    JpegQuality int `json:"jpeg_quality"`
}

var (
    Config Configuration
    Group *groupcache.Group
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
    if !ok { log.Fatalf("Loader %q doesn't exist.", Config.Loader) }

    renderer := &Renderer{
        Loader: loader(Config.LoaderArgs),
        MaxWidth: Config.MaxWidth,
        MaxHeight: Config.MaxHeight,
        MaxInDim: Config.MaxInDim,
        JpegQuality: Config.JpegQuality,
    }

    if Config.ListenGC == "" {
        if strings.HasPrefix(Config.Me,"http://") {
            Config.ListenGC = Config.Me[7:len(Config.Me)]
        }
    }
    peers := groupcache.NewHTTPPool(Config.Me)
    if len(Config.Peers) > 0 {
        peers.Set(Config.Peers...)
        go http.ListenAndServe(Config.ListenGC, http.HandlerFunc(peers.ServeHTTP))
    }
    Group = groupcache.NewGroup("bnw-thumb", 64<<20, renderer)

    //http.HandleFunc("/status", statusHandler)
    //http.HandleFunc("/", thumbHandler)
    fmt.Fprintf(os.Stderr, "Going to listen on %s\n", Config.Listen)
    server := &http.Server{Addr: Config.Listen, Handler: Handler{}}
    server.ListenAndServe()
}
