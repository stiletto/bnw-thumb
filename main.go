package main
import (
    "bytes"
    "crypto/sha1"
    "encoding/hex"
    "flag"
    "fmt"
    "image"
    "image/gif"
    "image/png"
    "image/jpeg"
    "io"
    "io/ioutil"
    "net/http"
    "os"
    "strconv"
    "strings"
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

func init() {
    flag.Uint64Var(&globalMaxWidth, "maxwidth", 512, "max thumbnail width in pixels")
    flag.Uint64Var(&globalMaxHeight, "maxheight", 512, "max thumbnail height in pixels")
    flag.Uint64Var(&maxInDim, "maxindim", 1024, "max input image height or width in pixels")
    flag.Uint64Var(&maxInSize, "maxinsize", 20*1024*1024, "max input image size in bytes")
    flag.IntVar(&jpegQuality, "quality", 80, "jpeg quality, 0 to 100")
    flag.StringVar(&errorPicName, "errorpic", "", "error picture")
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
        key := fmt.Sprintf("%s-%dx%d-%s", argFmt, maxWidth, maxHeight, hex.EncodeToString(h.Sum(nil)))[:250]
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
    hdr.Set("Cache-control", "max-age=5, public")
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

func renderWorker() {
    for job := range workChan {
        closeBody := false
        req, err := http.NewRequest("GET", job.Url, nil)
        if err != nil { goto Error }
        req.Header.Set("User-Agent", "bnw-thumb/1.0 (http://github.com/stiletto/bnw-thumb)")
        resp, err := hc.Do(req)
        if err != nil { goto Error }
        closeBody = true
        if resp.StatusCode != 200 || uint64(resp.ContentLength) > maxInSize { goto Error }
        bytebuf, err := ioutil.ReadAll(resp.Body)
        if err != nil { goto Error }
        bufreader := bytes.NewReader(bytebuf)
        config, _, err := image.DecodeConfig(bufreader)
        if err != nil || uint64(config.Width) > maxInDim || uint64(config.Height) > maxInDim { goto Error }
        bufreader.Seek(0,0)
        img, _, err := image.Decode(bufreader)
        if err != nil { goto Error }
        if closeBody { resp.Body.Close() }
        renderThumbnail(img, job.Key, time.Now(), job.MaxWidth, job.MaxHeight, job.Format)
        return
    Error:
        if closeBody { resp.Body.Close() }
        renderThumbnail(errorPic, job.Key, time.Now(), job.MaxWidth, job.MaxHeight, job.Format)
        return
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
    http.HandleFunc("/", thumbHandler)
    http.ListenAndServe(listenAddr, nil)
}
