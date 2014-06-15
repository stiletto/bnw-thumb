bnw-thumb
=========

Thumbnail generator application. Written in Go.

Copyright (C) 2014 Stiletto <blasux@blasux.ru>

This project is licensed under terms of MIT license.

How to use
==========
 1. Install Go, version 1.2 or higher
 2. Install and set up memcached.
 3. Install bnw-thumb (will fetch latest version from github and all of its dependencies) `$ GOPATH=/home/user/bnw-thumb go get github.com/stiletto/bnw-thumb`
 4. Launch bnw-thumb: `/home/user/bnw-thumb/bin/bnw-thumb -errorpic=/home/user/bnw-thumb/src/github.com/stiletto/bnw-thumb/1434.png -standbypic=/home/user/bnw-thumb/src/github.com/stiletto/bnw-thumb/256px-SMPTE_Color_Bars.svg.png -maxwidth=256 -maxheight=256 -maxindim=5120`
    "standbypic" is shown when thumbnail is being generated. "errorpic" is shown when thumbnail could not be generated.
    btw, there is -help, don't forget to look at it.
