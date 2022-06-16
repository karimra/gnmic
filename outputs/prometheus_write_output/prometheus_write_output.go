package prometheus_write_output

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/karimra/gnmic/formatters"
	"github.com/karimra/gnmic/outputs"
	"github.com/karimra/gnmic/types"
	"github.com/karimra/gnmic/utils"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/prompb"

	"github.com/prometheus/prometheus/model/labels"
	"google.golang.org/protobuf/proto"
)

const (
	outputType           = "prometheus_write"
	metricNameRegex      = "[^a-zA-Z0-9_]+"
	loggingPrefix        = "[prometheus_write_output:%s] "
	defaultTimeout       = 10 * time.Second
	defaultWriteInterval = 10 * time.Second
	defaultBufferSize    = 1000
	defaultMetricHelp    = "gNMIc generated metric"
	defaultNumWriters    = 1
	userAgent            = "gNMIc prometheus write"
)

func init() {
	outputs.Register(outputType,
		func() outputs.Output {
			return &promWriteOutput{
				Cfg:         &config{},
				logger:      log.New(io.Discard, loggingPrefix, utils.DefaultLoggingFlags),
				eventChan:   make(chan *formatters.EventMsg),
				buffCh:      make(chan struct{}),
				metricRegex: regexp.MustCompile(metricNameRegex),
			}
		})
}

type promWriteOutput struct {
	Cfg    *config
	logger *log.Logger

	httpClient   *http.Client
	eventChan    chan *formatters.EventMsg
	timeSeriesCh chan *prompb.TimeSeries
	buffCh       chan struct{}

	cfn         context.CancelFunc
	metricRegex *regexp.Regexp
	evps        []formatters.EventProcessor

	targetTpl *template.Template
	// TODO:
	// gnmiCache *cache.GnmiOutputCache
}

type config struct {
	Name            string            `mapstructure:"name,omitempty" json:"name,omitempty"`
	URL             string            `mapstructure:"url,omitempty" json:"url,omitempty"`
	Timeout         time.Duration     `mapstructure:"timeout,omitempty" json:"timeout,omitempty"`
	Headers         map[string]string `mapstructure:"headers,omitempty" json:"headers,omitempty"`
	Authentication  *auth             `mapstructure:"authentication,omitempty" json:"authentication,omitempty"`
	Authorization   *authorization    `mapstructure:"authorization,omitempty" json:"authorization,omitempty"`
	TLS             *tls              `mapstructure:"tls,omitempty" json:"tls,omitempty"`
	Interval        time.Duration     `mapstructure:"interval,omitempty" json:"interval,omitempty"`
	BufferSize      int               `mapstructure:"buffer-size,omitempty" json:"buffer-size,omitempty"`
	IncludeMetadata bool              `mapstructure:"include-metadata,omitempty" json:"include-metadata,omitempty"`
	NumWriters      int               `mapstructure:"num-writers,omitempty" json:"num-writers,omitempty"`
	Debug           bool              `mapstructure:"debug,omitempty" json:"debug,omitempty"`
	//
	MetricPrefix           string   `mapstructure:"metric-prefix,omitempty" json:"metric-prefix,omitempty"`
	AppendSubscriptionName bool     `mapstructure:"append-subscription-name,omitempty" json:"append-subscription-name,omitempty"`
	AddTarget              string   `mapstructure:"add-target,omitempty" json:"add-target,omitempty"`
	TargetTemplate         string   `mapstructure:"target-template,omitempty" json:"target-template,omitempty"`
	StringsAsLabels        bool     `mapstructure:"strings-as-labels,omitempty" json:"strings-as-labels,omitempty"`
	EventProcessors        []string `mapstructure:"event-processors,omitempty" json:"event-processors,omitempty"`
}

type auth struct {
	Username string `mapstructure:"username,omitempty" json:"username,omitempty"`
	Password string `mapstructure:"password,omitempty" json:"password,omitempty"`
}

type authorization struct {
	Type        string `mapstructure:"type,omitempty" json:"type,omitempty"`
	Credentials string `mapstructure:"credentials,omitempty" json:"credentials,omitempty"`
}

type tls struct {
	CAFile     string `mapstructure:"ca-file,omitempty" json:"ca-file,omitempty"`
	CertFile   string `mapstructure:"cert-file,omitempty" json:"cert-file,omitempty"`
	KeyFile    string `mapstructure:"key-file,omitempty" json:"key-file,omitempty"`
	SkipVerify bool   `mapstructure:"skip-verify,omitempty" json:"skip-verify,omitempty"`
}

func (p *promWriteOutput) Init(ctx context.Context, name string, cfg map[string]interface{}, opts ...outputs.Option) error {
	err := outputs.DecodeConfig(cfg, p.Cfg)
	if err != nil {
		return err
	}
	if p.Cfg.URL == "" {
		return errors.New("missing url field")
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.Cfg.Name == "" {
		p.Cfg.Name = name
	}
	p.logger.SetPrefix(fmt.Sprintf(loggingPrefix, p.Cfg.Name))
	if p.Cfg.TargetTemplate == "" {
		p.targetTpl = outputs.DefaultTargetTemplate
	} else if p.Cfg.AddTarget != "" {
		p.targetTpl, err = utils.CreateTemplate("target-template", p.Cfg.TargetTemplate)
		if err != nil {
			return err
		}
		p.targetTpl = p.targetTpl.Funcs(outputs.TemplateFuncs)
	}

	err = p.setDefaults()
	if err != nil {
		return err
	}

	// initialize buffer chan
	p.timeSeriesCh = make(chan *prompb.TimeSeries, p.Cfg.BufferSize)
	err = p.createHTTPClient()
	if err != nil {
		return err
	}

	ctx, p.cfn = context.WithCancel(ctx)
	go p.worker(ctx)
	for i := 0; i < p.Cfg.NumWriters; i++ {
		go p.writer(ctx)
	}

	p.logger.Printf("initialized prometheus_write output %s: %s", p.Cfg.Name, p.String())
	return nil
}

func (p *promWriteOutput) Write(ctx context.Context, rsp proto.Message, meta outputs.Meta) {
	if rsp == nil {
		return
	}
	switch rsp := rsp.(type) {
	case *gnmi.SubscribeResponse:
		measName := "default"
		if subName, ok := meta["subscription-name"]; ok {
			measName = subName
		}
		err := outputs.AddSubscriptionTarget(rsp, meta, p.Cfg.AddTarget, p.targetTpl)
		if err != nil {
			p.logger.Printf("failed to add target to the response: %v", err)
		}

		events, err := formatters.ResponseToEventMsgs(measName, rsp, meta, p.evps...)
		if err != nil {
			p.logger.Printf("failed to convert message to event: %v", err)
			return
		}
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return
			case p.eventChan <- ev:
			}
		}
	}
}

func (p *promWriteOutput) WriteEvent(ctx context.Context, ev *formatters.EventMsg) {
	select {
	case <-ctx.Done():
		return
	default:
		var evs = []*formatters.EventMsg{ev}
		for _, proc := range p.evps {
			evs = proc.Apply(evs...)
		}
		for _, pev := range evs {
			p.eventChan <- pev
		}
	}
}

func (p *promWriteOutput) Close() error {
	if p.cfn == nil {
		return nil
	}
	p.cfn()
	return nil
}

func (p *promWriteOutput) RegisterMetrics(*prometheus.Registry) {}

func (p *promWriteOutput) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (p *promWriteOutput) SetLogger(logger *log.Logger) {
	if logger != nil && p.logger != nil {
		p.logger.SetOutput(logger.Writer())
		p.logger.SetFlags(logger.Flags())
	}
}

func (p *promWriteOutput) SetEventProcessors(ps map[string]map[string]interface{},
	logger *log.Logger,
	tcs map[string]*types.TargetConfig,
	acts map[string]map[string]interface{}) {
	for _, epName := range p.Cfg.EventProcessors {
		if epCfg, ok := ps[epName]; ok {
			epType := ""
			for k := range epCfg {
				epType = k
				break
			}
			if in, ok := formatters.EventProcessors[epType]; ok {
				ep := in()
				err := ep.Init(epCfg[epType],
					formatters.WithLogger(logger),
					formatters.WithTargets(tcs),
					formatters.WithActions(acts))
				if err != nil {
					p.logger.Printf("failed initializing event processor '%s' of type='%s': %v", epName, epType, err)
					continue
				}
				p.evps = append(p.evps, ep)
				p.logger.Printf("added event processor '%s' of type=%s to prometheus output", epName, epType)
				continue
			}
			p.logger.Printf("%q event processor has an unknown type=%q", epName, epType)
			continue
		}
		p.logger.Printf("%q event processor not found!", epName)
	}
}

func (p *promWriteOutput) SetName(name string) {
	if p.Cfg.Name == "" {
		p.Cfg.Name = name
	}
}

func (p *promWriteOutput) SetClusterName(_ string) {}

func (p *promWriteOutput) SetTargetsConfig(map[string]*types.TargetConfig) {}

//

func (p *promWriteOutput) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-p.eventChan:
			if p.Cfg.Debug {
				p.logger.Printf("got event to buffer: %+v", ev)
			}
			for _, pt := range p.timeSeriesFromEvent(ev) {
				if len(p.timeSeriesCh) >= p.Cfg.BufferSize {
					if p.Cfg.Debug {
						p.logger.Printf("buffer size reached, triggering write")
					}
					p.buffCh <- struct{}{}
				}
				if p.Cfg.Debug {
					p.logger.Printf("writing TimeSeries to buffer")
				}
				p.timeSeriesCh <- pt
			}
		}
	}
}

func (p *promWriteOutput) setDefaults() error {
	if p.Cfg.Timeout <= 0 {
		p.Cfg.Timeout = defaultTimeout
	}
	if p.Cfg.Interval <= 0 {
		p.Cfg.Interval = defaultWriteInterval
	}
	if p.Cfg.BufferSize <= 0 {
		p.Cfg.BufferSize = defaultBufferSize
	}
	if p.Cfg.NumWriters <= 0 {
		p.Cfg.NumWriters = defaultNumWriters
	}
	return nil
}

func (p *promWriteOutput) getLabels(ev *formatters.EventMsg) []prompb.Label {
	labels := make([]prompb.Label, 0, len(ev.Tags))
	addedLabels := make(map[string]struct{})
	for k, v := range ev.Tags {
		labelName := p.metricRegex.ReplaceAllString(filepath.Base(k), "_")
		if _, ok := addedLabels[labelName]; ok {
			continue
		}
		labels = append(labels, prompb.Label{Name: labelName, Value: v})
		addedLabels[labelName] = struct{}{}
	}
	if !p.Cfg.StringsAsLabels {
		return labels
	}

	var err error
	for k, v := range ev.Values {
		_, err = getFloat(v)
		if err == nil {
			continue
		}
		if vs, ok := v.(string); ok {
			labelName := p.metricRegex.ReplaceAllString(filepath.Base(k), "_")
			if _, ok := addedLabels[labelName]; ok {
				continue
			}
			labels = append(labels, prompb.Label{Name: labelName, Value: vs})
		}
	}
	labels = append(labels, prompb.Label{})
	return labels
}

// metricName generates the prometheus metric name based on the output plugin,
// the measurement name and the value name.
// it makes sure the name matches the regex "[^a-zA-Z0-9_]+"
func (p *promWriteOutput) metricName(measName, valueName string) string {
	sb := strings.Builder{}
	if p.Cfg.MetricPrefix != "" {
		sb.WriteString(p.metricRegex.ReplaceAllString(p.Cfg.MetricPrefix, "_"))
		sb.WriteString("_")
	}
	if p.Cfg.AppendSubscriptionName {
		sb.WriteString(strings.TrimRight(p.metricRegex.ReplaceAllString(measName, "_"), "_"))
		sb.WriteString("_")
	}
	sb.WriteString(strings.TrimLeft(p.metricRegex.ReplaceAllString(valueName, "_"), "_"))
	return sb.String()
}

func (p *promWriteOutput) timeSeriesFromEvent(ev *formatters.EventMsg) []*prompb.TimeSeries {
	promTS := make([]*prompb.TimeSeries, 0, len(ev.Values))
	tsLabels := p.getLabels(ev)
	for k, v := range ev.Values {
		fv, err := getFloat(v)
		if err != nil {
			p.logger.Printf("failed to convert value %v to float: %v", v, err)
			continue
		}

		promTS = append(promTS,
			&prompb.TimeSeries{
				Labels: append(tsLabels,
					prompb.Label{
						Name:  labels.MetricName,
						Value: p.metricName(ev.Name, k),
					}),
				Samples: []prompb.Sample{
					{
						Value:     fv,
						Timestamp: ev.Timestamp / int64(time.Millisecond),
					},
				},
			})
	}
	return promTS
}

func getFloat(v interface{}) (float64, error) {
	switch i := v.(type) {
	case float64:
		return float64(i), nil
	case float32:
		return float64(i), nil
	case int64:
		return float64(i), nil
	case int32:
		return float64(i), nil
	case int16:
		return float64(i), nil
	case int8:
		return float64(i), nil
	case uint64:
		return float64(i), nil
	case uint32:
		return float64(i), nil
	case uint16:
		return float64(i), nil
	case uint8:
		return float64(i), nil
	case int:
		return float64(i), nil
	case uint:
		return float64(i), nil
	case string:
		f, err := strconv.ParseFloat(i, 64)
		if err != nil {
			return math.NaN(), err
		}
		return f, err
	case *gnmi.Decimal64:
		return float64(i.Digits) / math.Pow10(int(i.Precision)), nil
	default:
		return math.NaN(), errors.New("getFloat: unknown value is of incompatible type")
	}
}