// Copyright © 2020 Karim Radhouani <medkarimrdi@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/karimra/gnmic/collector"
	"github.com/karimra/gnmic/config"
	"github.com/karimra/gnmic/formatters"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/protobuf/proto"
)

const (
	defaultGrpcPort = "57400"
	msgSize         = 512 * 1024 * 1024
)

var encodingNames = []string{
	"json",
	"bytes",
	"proto",
	"ascii",
	"json_ietf",
}
var encodings = [][2]string{
	{"json", "JSON encoded string (RFC7159)"},
	{"bytes", "byte sequence whose semantics is opaque to the protocol"},
	{"proto", "serialised protobuf message using protobuf.Any"},
	{"ascii", "ASCII encoded string representing text formatted according to a target-defined convention"},
	{"json_ietf", "JSON_IETF encoded string (RFC7951)"},
}
var formatNames = []string{
	"json",
	"protojson",
	"prototext",
	"event",
	"proto",
}
var formats = [][2]string{
	{"json", "similar to protojson but with xpath style paths and decoded timestamps"},
	{"protojson", "protocol buffer messages in JSON format"},
	{"prototext", "protocol buffer messages in textproto format"},
	{"event", "protocol buffer messages as a timestamped list of tags and values"},
	{"proto", "protocol buffer messages in binary wire format"},
}
var tlsVersions = []string{"1.3", "1.2", "1.1", "1.0", "1"}

var cfgFile string

type CLI struct {
	config    *config.Config
	logger    *log.Logger
	collector *collector.Collector
}

var cli = &CLI{config: config.New()}

func rootCmdPersistentPreRunE(cmd *cobra.Command, args []string) error {
	err := cli.config.Load(cfgFile)
	if err != nil {
		return err
	}
	//postInitCommands(cmd.Commands())

	debug := cli.config.Debug
	loggingFlags := log.LstdFlags | log.Lmicroseconds
	if debug {
		loggingFlags |= log.Llongfile
	}
	cli.logger = log.New(os.Stderr, "gnmic ", loggingFlags)

	logFile := cli.config.LogFile
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("error opening log file: %v", err)
		}
		cli.logger.SetOutput(f)
	} else {
		if debug {
			cli.config.Log = true
		}
		if cli.config.Log {
			cli.logger.SetOutput(os.Stderr)
		} else {
			cli.logger.SetOutput(ioutil.Discard)
		}
	}
	if debug {
		grpclog.SetLogger(cli.logger) //lint:ignore SA1019 see https://github.com/karimra/gnmic/issues/59
		log.Printf("version=%s, commit=%s, date=%s, gitURL=%s, docs=https://gnmic.kmrd.dev", version, commit, date, gitURL)
	}
	cfgFile := cli.config.ConfigFileUsed()
	if len(cfgFile) != 0 {
		cli.logger.Printf("using config file %s", cfgFile)
		b, err := ioutil.ReadFile(cfgFile)
		if err != nil {
			if cmd.Flag("config").Changed {
				return err
			}
			cli.logger.Printf("failed reading config file: %v", err)
		}
		if debug {
			cli.logger.Printf("config file:\n%s", string(b))
		}
	}
	//logConfigKeysValues()
	return nil
}

// func logConfigKeysValues() {
// 	if cli.config.Debug {
// 		b, err := json.MarshalIndent(cli.config.AllSettings(), "", "  ")
// 		if err != nil {
// 			cli.logger.Printf("could not marshal config settings: %v", err)
// 		} else {
// 			cli.logger.Printf("set flags/config:\n%s\n", string(b))
// 		}
// 		keys := cli.config.AllKeys()
// 		sort.Strings(keys)

// 		for _, k := range keys {
// 			v := cli.config.Get(k)
// 			cli.logger.Printf("%s='%v'(%T)", k, v, v)
// 		}
// 	}
// }

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "gnmic",
	Short: "run gnmi rpcs from the terminal (https://gnmic.kmrd.dev)",
	Annotations: map[string]string{
		"--encoding": "ENCODING",
		"--config":   "FILE",
		"--format":   "FORMAT",
		"--address":  "TARGET",
	},
	PersistentPreRunE: rootCmdPersistentPreRunE,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		//fmt.Println(err)
		os.Exit(1)
	}
	if promptMode {
		ExecutePrompt()
	}
}

func init() {
	//cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/gnmic.yaml)")
	rootCmd.PersistentFlags().StringSliceP("address", "a", []string{}, "comma separated gnmi targets addresses")
	rootCmd.PersistentFlags().StringP("username", "u", "", "username")
	rootCmd.PersistentFlags().StringP("password", "p", "", "password")
	rootCmd.PersistentFlags().StringP("port", "", defaultGrpcPort, "gRPC port")
	rootCmd.PersistentFlags().StringP("encoding", "e", "json", fmt.Sprintf("one of %q. Case insensitive", encodingNames))
	rootCmd.PersistentFlags().BoolP("insecure", "", false, "insecure connection")
	rootCmd.PersistentFlags().StringP("tls-ca", "", "", "tls certificate authority")
	rootCmd.PersistentFlags().StringP("tls-cert", "", "", "tls certificate")
	rootCmd.PersistentFlags().StringP("tls-key", "", "", "tls key")
	rootCmd.PersistentFlags().DurationP("timeout", "", 10*time.Second, "grpc timeout, valid formats: 10s, 1m30s, 1h")
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "debug mode")
	rootCmd.PersistentFlags().BoolP("skip-verify", "", false, "skip verify tls connection")
	rootCmd.PersistentFlags().BoolP("no-prefix", "", false, "do not add [ip:port] prefix to print output in case of multiple targets")
	rootCmd.PersistentFlags().BoolP("proxy-from-env", "", false, "use proxy from environment")
	rootCmd.PersistentFlags().StringP("format", "", "", fmt.Sprintf("output format, one of: %q", formatNames))
	rootCmd.PersistentFlags().StringP("log-file", "", "", "log file path")
	rootCmd.PersistentFlags().BoolP("log", "", false, "show log messages in stderr")
	rootCmd.PersistentFlags().IntP("max-msg-size", "", msgSize, "max grpc msg size")
	rootCmd.PersistentFlags().StringP("prometheus-address", "", "", "prometheus server address")
	rootCmd.PersistentFlags().BoolP("print-request", "", false, "print request as well as the response(s)")
	rootCmd.PersistentFlags().DurationP("retry", "", defaultRetryTimer, "retry timer for RPCs")
	rootCmd.PersistentFlags().StringP("tls-min-version", "", "", fmt.Sprintf("minimum TLS supported version, one of %q", tlsVersions))
	rootCmd.PersistentFlags().StringP("tls-max-version", "", "", fmt.Sprintf("maximum TLS supported version, one of %q", tlsVersions))
	rootCmd.PersistentFlags().StringP("tls-version", "", "", fmt.Sprintf("set TLS version. Overwrites --tls-min-version and --tls-max-version, one of %q", tlsVersions))
	//
	rootCmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		cli.config.BindPFlag(flag.Name, flag)
	})

	rootCmd.AddCommand(newCapabilitiesCommand())
	rootCmd.AddCommand(newGetCommand())
	rootCmd.AddCommand(newListenCommand())
	rootCmd.AddCommand(newPathCommand())
	rootCmd.AddCommand(newSetCommand())
	rootCmd.AddCommand(newSubscribeCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newPromptCommand())
}

// func postInitCommands(commands []*cobra.Command) {
// 	for _, cmd := range commands {
// 		presetRequiredFlags(cmd)
// 		if cmd.HasSubCommands() {
// 			postInitCommands(cmd.Commands())
// 		}
// 	}
// }

// func presetRequiredFlags(cmd *cobra.Command) {
// 	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
// 		flagName := fmt.Sprintf("%s-%s", cmd.Name(), f.Name)
// 		value := cli.config.Get(flagName)
// 		if value != nil && cli.config.IsSet(flagName) && !f.Changed {
// 			var err error
// 			switch value := value.(type) {
// 			case string:
// 				err = cmd.LocalFlags().Set(f.Name, value)
// 			case []interface{}:
// 				ls := make([]string, len(value))
// 				for i := range value {
// 					ls[i] = value[i].(string)
// 				}
// 				err = cmd.LocalFlags().Set(f.Name, strings.Join(ls, ","))
// 			case []string:
// 				err = cmd.LocalFlags().Set(f.Name, strings.Join(value, ","))
// 			default:
// 				fmt.Printf("unexpected config value type, value=%v, type=%T\n", value, value)
// 			}
// 			if err != nil {
// 				fmt.Printf("failed setting flag '%s' from config: %v\n", flagName, err)
// 			}
// 		}
// 	})
// }

func loadCerts(tlscfg *tls.Config) error {
	if cli.config.TLSCert != "" && cli.config.TLSKey != "" {
		certificate, err := tls.LoadX509KeyPair(cli.config.TLSCert, cli.config.TLSKey)
		if err != nil {
			return err
		}
		tlscfg.Certificates = []tls.Certificate{certificate}
		tlscfg.BuildNameToCertificate()
	}
	return nil
}

func loadCACerts(tlscfg *tls.Config) error {
	certPool := x509.NewCertPool()
	if cli.config.TLSCa != "" {
		caFile, err := ioutil.ReadFile(cli.config.TLSCa)
		if err != nil {
			return err
		}
		if ok := certPool.AppendCertsFromPEM(caFile); !ok {
			return errors.New("failed to append certificate")
		}
		tlscfg.RootCAs = certPool
	}
	return nil
}

func printer(ctx context.Context, c chan string) {
	for {
		select {
		case m := <-c:
			fmt.Println(m)
		case <-ctx.Done():
			return
		}
	}
}
func gather(ctx context.Context, c chan string, ls *[]string) {
	for {
		select {
		case m := <-c:
			*ls = append(*ls, m)
		case <-ctx.Done():
			return
		}
	}
}

func indent(prefix, s string) string {
	if prefix == "" {
		return s
	}
	prefix = "\n" + strings.TrimRight(prefix, "\n")
	lines := strings.Split(s, "\n")
	return strings.TrimLeft(fmt.Sprintf("%s%s", prefix, strings.Join(lines, prefix)), "\n")
}

func filterModels(ctx context.Context, coll *collector.Collector, tName string, modelsNames []string) (map[string]*gnmi.ModelData, []string, error) {
	supModels, err := coll.GetModels(ctx, tName)
	if err != nil {
		return nil, nil, err
	}
	unsupportedModels := make([]string, 0)
	supportedModels := make(map[string]*gnmi.ModelData)
	var found bool
	for _, m := range modelsNames {
		found = false
		for _, tModel := range supModels {
			if m == tModel.Name {
				supportedModels[m] = tModel
				found = true
				break
			}
		}
		if !found {
			unsupportedModels = append(unsupportedModels, m)
		}
	}
	return supportedModels, unsupportedModels, nil
}

func setupCloseHandler(cancelFn context.CancelFunc) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-c
		fmt.Printf("\nreceived signal '%s'. terminating...\n", sig.String())
		cancelFn()
		os.Exit(0)
	}()
}

func numTargets() int {
	addressesLength := len(cli.config.Address)
	if addressesLength > 0 {
		return addressesLength
	}
	return len(cli.config.Targets)
}

func createCollectorDialOpts() []grpc.DialOption {
	opts := []grpc.DialOption{}
	opts = append(opts, grpc.WithBlock())
	if cli.config.MaxMsgSize > 0 {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(cli.config.MaxMsgSize)))
	}
	if !cli.config.ProxyFromEnv {
		opts = append(opts, grpc.WithNoProxy())
	}
	return opts
}

func printMsg(address string, msg proto.Message) error {
	printPrefix := ""
	if numTargets() > 1 && !cli.config.NoPrefix {
		printPrefix = fmt.Sprintf("[%s] ", address)
	}

	switch msg := msg.ProtoReflect().Interface().(type) {
	case *gnmi.CapabilityResponse:
		if len(cli.config.Format) == 0 {
			printCapResponse(printPrefix, msg)
			return nil
		}
	}
	mo := formatters.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
		Format:    cli.config.Format,
	}
	b, err := mo.Marshal(msg, map[string]string{"address": address})
	if err != nil {
		return err
	}
	sb := strings.Builder{}
	sb.Write(b)
	fmt.Printf("%s\n", indent(printPrefix, sb.String()))
	return nil
}

func printCapResponse(printPrefix string, msg *gnmi.CapabilityResponse) {
	sb := strings.Builder{}
	sb.WriteString(printPrefix)
	sb.WriteString("gNMI version: ")
	sb.WriteString(msg.GNMIVersion)
	sb.WriteString("\n")
	if cli.config.CapabilitiesVersion {
		return
	}
	sb.WriteString(printPrefix)
	sb.WriteString("supported models:\n")
	for _, sm := range msg.SupportedModels {
		sb.WriteString(printPrefix)
		sb.WriteString("  - ")
		sb.WriteString(sm.GetName())
		sb.WriteString(", ")
		sb.WriteString(sm.GetOrganization())
		sb.WriteString(", ")
		sb.WriteString(sm.GetVersion())
		sb.WriteString("\n")
	}
	sb.WriteString(printPrefix)
	sb.WriteString("supported encodings:\n")
	for _, se := range msg.SupportedEncodings {
		sb.WriteString(printPrefix)
		sb.WriteString("  - ")
		sb.WriteString(se.String())
		sb.WriteString("\n")
	}
	fmt.Printf("%s\n", indent(printPrefix, sb.String()))
}
