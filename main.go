package main
import (
    "bytes"
    "crypto/sha1"
    "encoding/hex"
    "encoding/json"
    "flag"
    "fmt"
    "image"
    "image/gif"
    "image/png"
    "image/jpeg"
    "io"
    "io/ioutil"
    "net"
    "net/http"
    "net/url"
    "os"
    "strconv"
    "strings"
    "sync/atomic"
    "time"
    "github.com/nfnt/resize"
    "github.com/bradfitz/gomemcache/memcache"
)

type JobDescription struct {
    Url string
    Key string
    Format string
    MaxHeight uint64
    MaxWidth uint64
}

var (
    globalMaxWidth uint64
    globalMaxHeight uint64
    maxInDim uint64
    maxInSize uint64
    errorPicName string
    standByPicName string
    listenAddr string
    memcacheAddr string
    jpegQuality int

    marshaledTimeLength int
    errorPic image.Image
    standByPic image.Image
    mc *memcache.Client
    hc http.Client
    workChan chan *JobDescription = make(chan *JobDescription, 100)
)

var formatsMime = map[string]string{
        "gif": "image/gif",
        "png": "image/png",
        "jpg": "image/jpeg",
}

type StatusStruct struct {
    FreeWorkers uint64
    QueueLen int
    RequestsProcessed uint64
    ThumbsGenerated uint64
    ThumbsFailed uint64
    WorkerStatus []*JobDescription
}
var status StatusStruct

func init() {
    flag.Uint64Var(&globalMaxWidth, "maxwidth", 512, "max thumbnail width in pixels")
    flag.Uint64Var(&globalMaxHeight, "maxheight", 512, "max thumbnail height in pixels")
    flag.Uint64Var(&maxInDim, "maxindim", 1024, "max input image height or width in pixels")
    flag.Uint64Var(&maxInSize, "maxinsize", 20*1024*1024, "max input image size in bytes")
    flag.IntVar(&jpegQuality, "quality", 80, "jpeg quality, 0 to 100")
    flag.StringVar(&errorPicName, "errorpic", "", "error picture")
    flag.StringVar(&standByPicName, "standbypic", "", "standby picture")
    flag.StringVar(&memcacheAddr, "memcache", "127.0.0.1:11211", "comma-separated list of memcache servers")
    flag.StringVar(&listenAddr, "listen", "127.0.0.1:8080", "address and port to listen on")
}

func parseOrDefault(s string, min uint64, max uint64, def uint64) uint64 {
    if s == "" {
        return def
    }
    n, err := strconv.ParseUint(s, 10, 64)
    switch {
        case err != nil:
            return def
        case n < min:
            return min
        case n > max:
            return max
    }
    return n
}

func encodeImage(w io.Writer, img image.Image, fmt string) error {
    switch fmt {
        case "png": return png.Encode(w, img)
        case "gif": return gif.Encode(w, img, &gif.Options{NumColors: 256})
    }
    return jpeg.Encode(w, img, &jpeg.Options{Quality: jpegQuality} )
}

func renderThumbnail(img image.Image, key string, lastMod time.Time,
                        maxWidth uint64, maxHeight uint64, fmt string) {
    thumb := resize.Thumbnail(uint(maxWidth), uint(maxHeight), img, resize.NearestNeighbor)
    var outbuf bytes.Buffer
    encodeImage(&outbuf, thumb, fmt)
    thumbHash := sha1.New()
    bytes.NewReader(outbuf.Bytes()).WriteTo(thumbHash)
    etag := hex.EncodeToString(thumbHash.Sum(nil))
    if len(etag) != 40 { return }
    saveThumb(key, []byte(etag), []byte{}, lastMod, outbuf.Bytes())
    return
}


func thumbHandler(w http.ResponseWriter, r *http.Request) {
    atomic.AddUint64(&status.RequestsProcessed, 1)
    if r.Method != "GET" {
        http.Error(w,"Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    r.ParseForm()
    var maxWidth uint64
    var maxHeight uint64
    argUrl := r.Form.Get("img")
    argFmt := r.Form.Get("fmt")
    mimeType,ok := formatsMime[argFmt]
    if !ok {
        argFmt = "png"
        mimeType = formatsMime["png"]
    }
    hdr := w.Header()
    hdr["Content-Type"] = []string{mimeType}
    argMax := r.Form.Get("max")
    if argMax != "" {
        maxWidth = parseOrDefault(argMax, 1, globalMaxWidth, globalMaxHeight)
        maxHeight = parseOrDefault(argMax, 1, globalMaxHeight, globalMaxHeight)
    } else {
        maxWidth = parseOrDefault(r.Form.Get("mx"), 1, globalMaxWidth, globalMaxWidth)
        maxHeight = parseOrDefault(r.Form.Get("my"), 1, globalMaxHeight, globalMaxHeight)
    }
    if mc != nil {
        h := sha1.New()
        io.WriteString(h, argUrl)
        key := fmt.Sprintf("%s-%dx%d-%s", argFmt, maxWidth, maxHeight, hex.EncodeToString(h.Sum(nil)))
        if len(key)> 250 { key=key[:250] }
        item, err := mc.Get(key)
        if err == nil { 
            if len(item.Value) >= 86 {
                thumbEtag := item.Value[0:40]
                //originalEtag := item.Value[40:80]
                lastModBytes := item.Value[80:80+marshaledTimeLength]
                lastMod := time.Time{}
                lastMod.UnmarshalBinary(lastModBytes)
                dataBytes := item.Value[80+marshaledTimeLength:]
                hdr.Set("Last-Modified", lastMod.Format(http.TimeFormat))
                hdr.Set("Etag", string(thumbEtag))
                hdr.Set("Content-Length", strconv.Itoa(len(dataBytes)))
                hdr.Set("Cache-control", "max-age=604800, public")
                w.Write(dataBytes)
                return
            }
        } else if err == memcache.ErrCacheMiss {
            err = mc.Add(&memcache.Item{Key: key, Value: []byte("X")})
            if err != memcache.ErrNotStored {
                workChan <- &JobDescription{
                    Url: argUrl, Key: key, Format: argFmt,
                    MaxHeight: maxHeight, MaxWidth: maxWidth,
                }
            }
        }
    }

    thumb := resize.Thumbnail(uint(maxWidth), uint(maxHeight), standByPic, resize.NearestNeighbor)
    hdr.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
    hdr.Set("Cache-control", "max-age=5, public, must-revalidate")
    hdr.Set("Etag", fmt.Sprintf("standby-%d-%d-%s", maxWidth, maxHeight, argFmt))
    encodeImage(w, thumb, argFmt)
    return
}

func saveThumb(Key string, Etag []byte, OriginEtag []byte, LastMod time.Time, data []byte) error {
    if mc != nil {
        value := make([]byte, 0, 128)
        lastModBytes, _ := LastMod.MarshalBinary()
        OriginEtag := []byte("0000000000000000000000000000000000000000") // not used yet
        value = append(value, Etag...)
        value = append(value, OriginEtag...)
        value = append(value, lastModBytes...)
        value = append(value, data...)
        return mc.Set(&memcache.Item{Key: Key, Value: value })
    }
    return nil
}


func statusHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != "GET" {
        http.Error(w,"Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    r.ParseForm()
    argFormat := r.Form.Get("format")

    hdr := w.Header()

    status.FreeWorkers = 0
    for _, v:= range status.WorkerStatus {
        if v == nil {
            status.FreeWorkers += 1
        }
    }

    status.QueueLen = len(workChan)
    if argFormat == "json" {
        hdr.Set("Content-Type", "application/json")
        stjson, _ := json.Marshal(status)
        w.Write(stjson)
    } else if argFormat == "jsonindent" {
        hdr.Set("Content-Type", "application/json")
        stjson, _ := json.MarshalIndent(status,"", "  ")
        w.Write(stjson)
    } else {
        hdr.Set("Content-Type", "text/html")
        fmt.Fprintf(w, "<html><body><pre>\n")
        fmt.Fprintf(w, "Free workers: %d/%d\n", status.FreeWorkers, 10)
        fmt.Fprintf(w, "Queue len: %d\n", status.QueueLen)
        fmt.Fprintf(w, "Requests processed: %d\n", status.RequestsProcessed)
        fmt.Fprintf(w, "Thumbnails generated/failed: %d/%d\n", status.ThumbsGenerated, status.ThumbsFailed)
        fmt.Fprintf(w, "Workers:\n")
        for k, v:= range status.WorkerStatus {
            if v == nil {
                status.FreeWorkers += 1
                fmt.Fprintf(w, " - %d - Free\n", k)
            } else {
                fmt.Fprintf(w, " - %d - %s\n", k, v.Url)
            }
        }
        fmt.Fprintf(w, "</pre></body></html>")
    }
}

func renderWorker(currentJob **JobDescription) {
    for job := range workChan {
        *currentJob = job
        fmt.Fprintf(os.Stderr, "Generating \"%s\" %s %dx%d\n", job.Url, job.Format, job.MaxWidth, job.MaxHeight)
        req, err := http.NewRequest("GET", job.Url, nil)
        addst := ""
        if err == nil {
            req.Header.Set("User-Agent", "bnw-thumb/1.0 (http://github.com/stiletto/bnw-thumb)")
            closeBody := false
            resp, err := hc.Do(req)
            if err == nil {
                closeBody = true
                if resp.StatusCode == 200 || uint64(resp.ContentLength) <= maxInSize {
                    bytebuf, err := ioutil.ReadAll(resp.Body)
                    if err == nil {
                        bufreader := bytes.NewReader(bytebuf)
                        config, _, err := image.DecodeConfig(bufreader)
                        if err == nil && uint64(config.Width) <= maxInDim && uint64(config.Height) <= maxInDim {
                            bufreader.Seek(0,0)
                            img, _, err := image.Decode(bufreader)
                            if err == nil {
                                fmt.Fprintf(os.Stderr, "Generated \"%s\" %s %dx%d\n", job.Url, job.Format, job.MaxWidth, job.MaxHeight)
                                if closeBody { resp.Body.Close() }
                                renderThumbnail(img, job.Key, time.Now(), job.MaxWidth, job.MaxHeight, job.Format)
                                atomic.AddUint64(&status.ThumbsGenerated, 1)
                                *currentJob = nil
                                continue
                            }
                        }
                    }
                }
            }
            switch e := err.(type) {
                case *url.Error:
                    addst = fmt.Sprintf("URL Error: %#v", e.Err)
                default:
                    addst = fmt.Sprintf("%#v", e)
            }
            if resp != nil {
                addst = fmt.Sprintf("%d, %d, %#v", resp.StatusCode, resp.ContentLength, addst )
            }
            fmt.Fprintf(os.Stderr, "Failed to generate \"%s\" %s %dx%d (%s)\n", job.Url, job.Format,
                        job.MaxWidth, job.MaxHeight, addst)
            if closeBody { resp.Body.Close() }
        }
        renderThumbnail(errorPic, job.Key, time.Now(), job.MaxWidth, job.MaxHeight, job.Format)
        atomic.AddUint64(&status.ThumbsFailed, 1)
        *currentJob = nil
    }
}

func loadPicOrEmpty(name string) image.Image {
    var pic image.Image
    if name != "" {
        var fmtinfo string
        img, err := os.Open(name)
        defer img.Close()
        if err == nil {
            pic, fmtinfo, err = image.Decode(img)
        }
        if err != nil {
            pic = nil
            fmt.Fprintf(os.Stderr, "Unable to load picture %s: %s\n", name, err)
        } else {
            ebounds := pic.Bounds()
            fmt.Fprintf(os.Stderr, "Loaded picture %s (%s, %dx%d)\n",
                name, fmtinfo, ebounds.Max.X-ebounds.Min.X,
                ebounds.Max.Y-ebounds.Min.Y)
        }
    }
    if pic == nil {
        pic = image.NewRGBA(image.Rect(0, 0, 1, 1))
    }
    return pic
}

func main() {
    flag.Parse()
    mc = nil
    if memcacheAddr != "" {
        mc = memcache.New(strings.Split(memcacheAddr, ",")...)
    } else {
        fmt.Fprintf(os.Stderr, "Warning: Memcache is required for bnw-thumb to work.\n")
        fmt.Fprintf(os.Stderr, "Warning: Without memcache no thumbnails will be actually generated. Use only for testing.\n")
    }
    marshaledNow, _ := time.Now().MarshalBinary()
    // we know that time.Time always has the same length when marshaled
    // but this is kinda implementation dependant hack
    marshaledTimeLength = len(marshaledNow)
    errorPic = loadPicOrEmpty(errorPicName)
    standByPic = loadPicOrEmpty(standByPicName)
    http.HandleFunc("/status", statusHandler)
    http.HandleFunc("/", thumbHandler)
    status.WorkerStatus = make([]*JobDescription, 10)
    for i := 0; i < 10; i++ {
        go renderWorker(&status.WorkerStatus[i])
    }
    hc.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment,
                                   ResponseHeaderTimeout: 10*time.Second,
                                   Dial: func (network, address string) (net.Conn, error) {
                                        return net.DialTimeout(network, address, 10*time.Second) }}
    fmt.Fprintf(os.Stderr, "Going to listen on %s\n", listenAddr)
    http.ListenAndServe(listenAddr, nil)
}
