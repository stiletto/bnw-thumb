package main
import (
    "errors"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "net"
    "net/http"
    "strconv"
    "time"
)

type HttpLoader struct {
    hc http.Client
    maxsize int64
}

type HttpLoaderError struct {
    Where string
    What error
    Url string
}
func (le *HttpLoaderError) Error() string {
    return fmt.Sprintf("%s: %#v", le.Where, le.What)
}

func NewHttpLoader(args map[string]string) SourceLoader {
    itimeout, err := strconv.ParseInt(args["timeout"], 10, 64)
    if err != nil {
        log.Printf("HttpLoader: Unable to parse timeout value '%s', using default of 10 seconds.", args["timeout"])
        itimeout = 10
    }
    max_size, err := strconv.ParseInt(args["max_size"], 10, 64)
    if err != nil {
        log.Printf("HttpLoader: Unable to parse max size value '%s', using 20 Mb.", args["max_size"])
        max_size = 20*1024*1024
    }
    timeout := time.Duration(itimeout)*time.Second

    l := &HttpLoader{}
    l.maxsize = max_size
    l.hc.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment,
                                   ResponseHeaderTimeout: timeout,
                                   TLSHandshakeTimeout: timeout,
                                   Dial: func (network, address string) (net.Conn, error) {
                                        return net.DialTimeout(network, address, timeout) }}

    return l
}

func (l *HttpLoader) Load(url string) ([]byte, error) {
    req, err := http.NewRequest("GET", url, nil)
    if err != nil { return nil, &HttpLoaderError{ "NewRequest", err, url } }

    req.Header.Set("User-Agent", "bnw-thumb/1.1 (http://github.com/stiletto/bnw-thumb)")
    resp, err := l.hc.Do(req)
    if err != nil { return nil, &HttpLoaderError{ "Do", err, url } }
    defer resp.Body.Close()

    if resp.StatusCode != 200 { return nil, &HttpLoaderError{ "NotOk", errors.New(strconv.Itoa(resp.StatusCode)), url } }
    if resp.ContentLength > l.maxsize { return nil, &HttpLoaderError{ "TooBig", errors.New(fmt.Sprintf("%d",resp.ContentLength)), url } }

    limiter := &io.LimitedReader{ R: resp.Body, N: l.maxsize+1 }
    bytebuf, err := ioutil.ReadAll(limiter)
    if err != nil { return nil, &HttpLoaderError{ "ReadAll", err, url } }
    if limiter.N == 0 { return nil, &HttpLoaderError{ "TooBig", errors.New(strconv.Itoa(int(l.maxsize+1))), url } }

    return bytebuf, nil
}

func init() {
    RegisterLoader("http", NewHttpLoader)
}
