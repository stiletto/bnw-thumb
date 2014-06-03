package main
import (
    "crypto/sha1"
    "encoding/hex"
    "flag"
    "fmt"
    "image"
    "io"
    "io/ioutil"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"
    "github.com/nfnt/resize"
    "github.com/bradfitz/gomemcache/memcache"
    _ "image/gif"
    _ "image/png"
    _ "image/jpeg"
)

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

    errorPic image.Image
    standByPic image.Image
    mc *memcache.Client
    formatsMime map[string]string {
        "gif", "image/gif",
        "png", "image/png",
        "jpg", "image/jpeg",
    }
    hc http.Client
)

func init() {
    flag.Uint64Var(&globalMaxWidth, "maxwidth", 512, "max thumbnail width in pixels")
    flag.Uint64Var(&globalMaxHeight, "maxheight", 512, "max thumbnail height in pixels")
    flag.Uint64Var(&maxInDim, "maxindim", 1024, "max input image height or width in pixels")
    flag.Uint64Var(&maxInSize, "maxinsize", 20*1024*1024, "max input image size in bytes")
    flag.IntVar(&jpegQuality, "quality", "", "jpeg quality, 0 to 100")
    flag.StringVar(&errorPicName, "errorpic", "", "error picture")
    flag.StringVar(&memcacheAddr, "memcache", "", "comma-separated list of memcache servers (no cache if empty)")
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

func encodeImage(w io.Writer, img image.Image, fmt string) error.error {
    switch fmt {
        case "png": return png.Encode(w, img)
        case "gif": return jpeg.Encode(w, img)
    }
    return jpeg.Encode(w, img, jpeg.Options{Quality: jpegQuality} )
}

func renderThumbnail(w http.ResponseWriter, img image.Image, maxWidth uint64, maxHeight uint64, fmt string, store bool) {
    thumb := resize.Thumbnail(maxWidth, maxHeight, img, resize.NearestNeighbor)
    var outbuf bytes.Buffer
    encodeImage(outbuf, thumb, argFmt)
    if store && mc != nil {
        mc.Set(&memcache.Item{Key: key, Value: outbuf.Bytes()})
    }
    hdr := w.Header()
    now := time.Now()
    hdr.Set("Last-Modified", now.UTC().Format(TimeFormat))
    return
}

func thumbHandler(w http.ResponseWriter, r *http.Request) {
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
    w.Header()["Content-Type"] = mimeType
    if !ok {
        argFmt = "png"
        memeType = formatsMime["png"]
    }
    argMax := r.Form.Get("max")
    if argMax != "" {
        maxWidth = parseOrDefault(argMax, 1, globalMaxWidth, globalMaxHeight)
        maxHeight = parseOrDefault(argMax, 1, globalMaxHeight, globalMaxHeight)
    } else {
        maxWidth = parseOrDefault(r.Form.Get("mx"), 1, globalMaxWidth, globalMaxWidth)
        maxHeight = parseOrDefault(r.Form.Get("my"), 1, globalMaxHeight, globalMaxHeight)
    }
    h := sha1.New()
    io.WriteString(h, argUrl)
    key := fmt.Sprintf("%s-%dx%d-%s", argFmt, maxWidth, maxHeight, hex.EncodeToString(h.Sum(nil)))[:250]
    if mc != nil {
        item, err := mc.Get(key)
        standby := false
        if err == nil { 
            if len(item.Value) != 1 {
                w.Header()["Content-Length"] = len(item.Value)
                w.Write(item.Value)
                return
            }
            standby = true
        } else if err == memcache.ErrCacheMiss {
            err = mc.Add(&memcache.Item{Key: key, Value: []byte("X")})
            if err == memcache.ErrNotStored { standby = true }
        }
        if standby {
            w.Header()["
            renderThumbnail(w, standByPic, maxWidth, maxHeight, argFmt, false)
            return
        }
    }
    req, err := http.NewRequest("GET", argUrl, nil)
    if err != nil { goto Error }
    req.Header.Set("User-Agent", "bnw-thumb/1.0 (http://github.com/stiletto/bnw-thumb)")
    resp, err := hc.Do(req)
    if err != nil { goto Error }
    defer resp.Body.Close()
    if resp.StatusCode != 200 || resp.ContentLength > maxInSize { goto Error }
    var buf bytes.Buffer
    b.Grow(resp.ContentLength)
    bytebuf, err := ioutil.ReadAll(resp.Body)
    if err != nil { goto Error }
    bufreader := bytes.NewReader(bytebuf)
    config, _, err := image.DecodeConfig(bufreader)
    if err != nil || config.Width > maxInDim || config.Height > maxInDim { goto Error }
    bufreader.Seek(0,0)
    img, _, err := image.Decode(bufreader)
    if err != nil { goto Error }
    renderThumbnail(w, img, maxWidth, maxHeight, argFmt, true)
    return
Error:
    renderThumbnail(w, errorPic, maxWidth, maxHeight, argFmt, true)
    return
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
    }
    errorPic = loadPicOrEmpty(errorPicName)
    standByPic = loadPicOrEmpty(standByPicName)
    http.HandleFunc("/", thumbHandler)
    http.ListenAndServe(listenAddr, nil)
}
