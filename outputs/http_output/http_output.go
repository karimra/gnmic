package http_output

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/karimra/gnmic/collector"
	"github.com/karimra/gnmic/outputs"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/http2"
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
	Url        string        `mapstructure:"url,omitempty"` // with port
	Rate       time.Duration `mapstructure:"rate,omitempty"`
	BufferSize uint          `mapstructure:"buffer-size,omitempty"`
	Format     string        `mapstructure:"format,omitempty"`

	Secure             bool                `mapstructure:"secure,omitempty"`
	Method             string              `mapstructure:"method,omitempty"`
	Headers            map[string][]string `mapstructure:"headers,omitempty"`
	TLSCert            string              `mapstructure:"tls_cert,omitempty"`
	InsecureSkipVerify bool                `mapstructure:"insecure_skip_verify,omitempty"`
	Timeout            time.Duration       `mapstructure:"timeout,omitempty"`

	// internals
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
		Transport: &http2.Transport{
			AllowHTTP: true,
		},
	}
	if u.Cfg.Secure {
		u.client.Transport = &http2.Transport{
			AllowHTTP: true,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	for k, vs := range u.Cfg.Headers {
		for _, v := range vs {
			u.Cfg.httpHeader.Set(k, v)
		}
	}
	if u.Cfg.Method == "" {
		u.Cfg.Method = "POST"
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
			req, err := http.NewRequestWithContext(ctx, u.Cfg.Method, u.Cfg.Url, bytes.NewBuffer(b))
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

func (u *HTTPOutput) tlsConfig() *tls.Config {
	crt, err := ioutil.ReadFile(u.Cfg.TLSCert)
	if err != nil {
		log.Printf("failed reading cert file '%s': %v", u.Cfg.TLSCert, err)
		return nil
	}

	rootCAs := x509.NewCertPool()
	rootCAs.AppendCertsFromPEM(crt)

	return &tls.Config{
		RootCAs:            rootCAs,
		InsecureSkipVerify: u.Cfg.InsecureSkipVerify,
	}
}
