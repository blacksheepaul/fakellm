package main

import (
	"flag"
	"log"

	"fakellm/internal/admin"
	"fakellm/internal/admission"
	"fakellm/internal/config"
	"fakellm/internal/handler"
	"fakellm/internal/queue"
	"fakellm/internal/tokenstream"

	"github.com/cloudwego/hertz/pkg/app/server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	cfg := config.NewManager(config.LoadFromEnv())

	// Build shared components from initial config.
	c := cfg.Load()
	sema := admission.New(c.MaxConcurrent)
	q := queue.New(c.MaxQueueDepth, 64) // 64 worker goroutines
	streamer := tokenstream.New(cfg, sema, q)

	h := handler.New(cfg, sema, q, streamer)
	adm := admin.New(cfg, sema, q, streamer)

	srv := server.New(
		server.WithHostPorts(*addr),
		server.WithStreamBody(true),
	)

	srv.POST("/v1/chat/completions", h.ChatCompletions)
	srv.GET("/admin/config", adm.GetConfig)
	srv.PATCH("/admin/config", adm.PatchConfig)
	srv.GET("/admin/stats", adm.GetStats)

	log.Printf("fakellm listening on %s", *addr)
	srv.Spin()
}
