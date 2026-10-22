// gmservice hosts administrator sessions, PVF catalogs and benefit scheduling
// outside the game host. It has no database configuration or database access.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	dnfpvf "dnf90.local/gateway/internal/platform/pvf"
	"dnf90.local/gateway/internal/servicehttp"
	"dnf90.local/gateway/internal/services/gmtool"
)

type config struct {
	QueuePath        string             `json:"queuePath"`
	SessionStorePath string             `json:"sessionStorePath,omitempty"`
	Listen           string             `json:"listen"`
	CatalogListen    string             `json:"catalogListen"`
	PublicOrigin     string             `json:"publicOrigin"`
	LoginKeySHA256   string             `json:"loginKeySha256"`
	WebRoot          string             `json:"webRoot"`
	PVFPath          string             `json:"pvfPath"`
	PVFIndexPath     string             `json:"pvfIndexPath"`
	PVFModeStatePath string             `json:"pvfModeStatePath,omitempty"`
	ItemIndexPath    string             `json:"itemIndexPath"`
	CatalogToken     string             `json:"catalogToken"`
	Gateway          servicehttp.Config `json:"gateway"`
	ScanSeconds      int                `json:"scanSeconds"`
	ScanEnabled      bool               `json:"scanEnabled"`
}

func readConfig(path string) (config, error) {
	var c config
	f, e := os.Open(path)
	if e != nil {
		return c, errors.New("GM service config unavailable")
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return c, errors.New("invalid GM service config")
	}
	for _, addr := range []string{c.Listen, c.CatalogListen} {
		host, _, e := net.SplitHostPort(addr)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return c, errors.New("GM service listeners must be loopback")
		}
	}
	u, e := url.Parse(c.PublicOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(c.LoginKeySHA256) != 64 || len(c.CatalogToken) < 32 || c.ScanSeconds < 5 || c.ScanSeconds > 3600 {
		return c, errors.New("invalid GM service identity or schedule")
	}
	return c, nil
}
func main() {
	path := flag.String("config", "", "protected service configuration")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := run(ctx, *path); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(ctx context.Context, path string) error {
	c, e := readConfig(path)
	if e != nil {
		return e
	}
	peer, e := servicehttp.New(c.Gateway)
	if e != nil {
		return e
	}
	if c.PVFModeStatePath == "" {
		c.PVFModeStatePath = c.QueuePath + ".pvf-mode.json"
	}
	source, e := dnfpvf.OpenModeSource(ctx, c.PVFPath, c.PVFIndexPath, c.Gateway.PVFSHA256, c.PVFModeStatePath)
	if e != nil {
		return e
	}
	defer source.Close()
	logger := func(message string, args ...any) { log.Println(append([]any{message}, args...)...) }
	if c.SessionStorePath == "" {
		c.SessionStorePath = c.QueuePath + ".sessions.json"
	}
	s, e := gmtool.NewCatalog(ctx, gmtool.Config{Password: c.LoginKeySHA256, LoginKeySHA256: c.LoginKeySHA256, SessionStorePath: c.SessionStorePath, ItemIndexCachePath: c.ItemIndexPath, Logger: logger}, source)
	if e != nil {
		return e
	}
	queue, e := gmtool.OpenExternalQueue(c.QueuePath, peer)
	if e != nil {
		return e
	}
	queue.ConfigureBenefitReconciliation(c.ScanEnabled)
	handlers := []http.Handler{s.ExternalHandler(c.WebRoot, c.PublicOrigin, peer, queue), s.CatalogHandler(c.CatalogToken)}
	servers := make([]*http.Server, 0, 2)
	failures := make(chan error, 3)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i, addr := range []string{c.Listen, c.CatalogListen} {
		srv := &http.Server{Addr: addr, Handler: handlers[i], ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		ln, e := net.Listen("tcp", addr)
		if e != nil {
			for _, old := range servers {
				old.Close()
			}
			return e
		}
		servers = append(servers, srv)
		go func() { failures <- srv.Serve(ln) }()
	}
	queueDone := make(chan struct{})
	go func() { defer close(queueDone); queue.Run(workerCtx, logger) }()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if c.ScanEnabled {
			if e := queue.RunBenefitReconciliation(workerCtx, time.Duration(c.ScanSeconds)*time.Second, logger); e != nil {
				failures <- e
			}
		}
	}()
	log.Println("external GM service ready")
	select {
	case <-ctx.Done():
	case e = <-failures:
	}
	cancel()
	shutdown, finish := context.WithTimeout(context.Background(), 30*time.Second)
	defer finish()
	for _, srv := range servers {
		srv.Shutdown(shutdown)
	}
	<-workerDone
	<-queueDone
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
