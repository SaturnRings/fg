package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/saturn/fg"
	"github.com/valyala/fasthttp"
)

var (
	addr     = flag.String("addr", ":4243", "TCP address to listen to")
	compress = flag.Bool("compress", false, "Whether to enable transparent response compression")
)

func requestHandler(ctx *fasthttp.RequestCtx) {
	time.Sleep(5 * time.Second)
	fmt.Fprintf(ctx, "Hello~")
}

func main() {
	flag.Parse()
	h := requestHandler
	if *compress {
		h = fasthttp.CompressHandler(h)
	}
	// grace fasthttp
	if err := fg.ListenAndServe(*addr, h); err != nil {
		// fast http
		//if err := fasthttp.ListenAndServe(*addr, h); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}
