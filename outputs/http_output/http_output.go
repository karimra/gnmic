package http_output

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/karimra/gnmic/collector"
	"github.com/karimra/gnmic/outputs"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

func init() {
	outputs.Register("http", func() outputs.Output {
		return &HTTPOutput{
			Cfg: &Config{},
		}
	})
}

type HTTPOutput struct {
	Cfg *Config

	client   *http.Client
	cancelFn context.CancelFunc
	buffer   chan []byte
	limiter  *time.Ticker
	logger   *log.Logger
	mo       *collector.MarshalOptions
}

type Config struct {
	Address    string        `mapstructure:"address,omitempty"` // ip:port
	Rate       time.Duration `mapstructure:"rate,omitempty"`
	BufferSize uint          `mapstructure:"buffer-size,omitempty"`
	Format     string        `mapstructure:"format,omitempty"`

	Schema  string              `mapstructure:"schema,omitempty"`
	Method  string              `mapstructure:"method,omitempty"`
	Path    string              `mapstructure:"path,omitempty"`
	Headers map[string][]string `mapstructure:"headers,omitempty"`
	Timeout time.Duration       `mapstructure:"timeout,omitempty"`

	// internals
	url        string
	httpHeader http.Header
}

func (u *HTTPOutput) Init(cfg map[string]interface{}, logger *log.Logger) error {
	decoder, err := mapstructure.NewDecoder(
		&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.StringToTimeDurationHookFunc(),
			Result:     u.Cfg,
		},
	)
	if err != nil {
		return err
	}
	err = decoder.Decode(cfg)
	if err != nil {
		return err
	}
	_, _, err = net.SplitHostPort(u.Cfg.Address)
	if err != nil {
		return fmt.Errorf("wrong address format: %v", err)
	}
	u.logger = log.New(os.Stderr, "http_output ", log.LstdFlags|log.Lmicroseconds)
	if logger != nil {
		u.logger.SetOutput(logger.Writer())
		u.logger.SetFlags(logger.Flags())
	}
	u.buffer = make(chan []byte, u.Cfg.BufferSize)
	if u.Cfg.Rate > 0 {
		u.limiter = time.NewTicker(u.Cfg.Rate)
	}
	if u.Cfg.Timeout == 0 {
		u.Cfg.Timeout = 10 * time.Second
	}

	var ctx context.Context
	ctx, u.cancelFn = context.WithCancel(context.Background())
	u.mo = &collector.MarshalOptions{Format: u.Cfg.Format}
	u.client = &http.Client{
		Timeout: u.Cfg.Timeout,
	}
	//
	if u.Cfg.Schema == "" {
		u.Cfg.Schema = "http"
	}
	if !strings.HasPrefix(u.Cfg.Schema, "https") {
		u.client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	if !strings.HasSuffix(u.Cfg.Schema, "://") {
		u.Cfg.Schema += "://"
	}
	u.Cfg.url = fmt.Sprintf("%s%s/%s", u.Cfg.Schema, u.Cfg.Address, u.Cfg.Path)
	for k, vs := range u.Cfg.Headers {
		for _, v := range vs {
			u.Cfg.httpHeader.Set(k, v)
		}
	}
	go u.start(ctx)
	return nil
}
func (u *HTTPOutput) Write(m proto.Message, meta outputs.Meta) {
	if m == nil {
		return
	}
	b, err := u.mo.Marshal(m, meta)
	if err != nil {
		u.logger.Printf("failed marshaling proto msg: %v", err)
		return
	}
	u.buffer <- b
}
func (u *HTTPOutput) Close() error {
	u.cancelFn()
	u.limiter.Stop()
	close(u.buffer)
	return nil
}
func (u *HTTPOutput) Metrics() []prometheus.Collector { return nil }
func (u *HTTPOutput) String() string {
	b, err := json.Marshal(u)
	if err != nil {
		return ""
	}
	return string(b)
}
func (u *HTTPOutput) start(ctx context.Context) {
	defer u.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-u.buffer:
			if u.limiter != nil {
				<-u.limiter.C
			}
			req, err := http.NewRequestWithContext(ctx, u.Cfg.Method, u.Cfg.url, bytes.NewBuffer(b))
			if err != nil {
				u.logger.Printf("failed creating http request %v", err)
				continue
			}
			req.Header = u.Cfg.httpHeader
			resp, err := u.client.Do(req)
			if err != nil {
				u.logger.Printf("failed sending http request %v", err)
				continue
			}
			if resp.StatusCode > 202 {
				log.Printf("http response status code=%d", resp.StatusCode)
			}
		}
	}
}
