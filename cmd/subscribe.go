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
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/karimra/gnmic/collector"
	"github.com/karimra/gnmic/formatters"
	_ "github.com/karimra/gnmic/outputs/all"
	"github.com/manifoldco/promptui"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const defaultRetryTimer = 10 * time.Second

var subscriptionModes = [][2]string{
	{"once", "a single request/response channel. The target creates the relevant update messages, transmits them, and subsequently closes the RPC"},
	{"stream", "long-lived subscriptions which continue to transmit updates relating to the set of paths that are covered within the subscription indefinitely"},
	{"poll", "on-demand retrieval of data items via long-lived RPCs"},
}

var streamSubscriptionModes = [][2]string{
	{"target-defined", "the target MUST determine the best type of subscription to be created on a per-leaf basis"},
	{"sample", "the value of the data item(s) MUST be sent once per sample interval to the client"},
	{"on-change", "data updates are only sent when the value of the data item changes"},
}

// used to init or reset subscribeCmd flags for gnmic-prompt mode
func initSubscribeFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("prefix", "", "", "subscribe request prefix")
	cmd.Flags().StringArrayVarP(&paths, "path", "", []string{}, "subscribe request paths")
	//cmd.MarkFlagRequired("path")
	cmd.Flags().Uint32P("qos", "q", 0, "qos marking")
	cmd.Flags().BoolP("updates-only", "", false, "only updates to current state should be sent")
	cmd.Flags().StringP("mode", "", "stream", "one of: once, stream, poll")
	cmd.Flags().StringP("stream-mode", "", "target-defined", "one of: on-change, sample, target-defined")
	cmd.Flags().DurationP("sample-interval", "i", 0,
		"sample interval as a decimal number and a suffix unit, such as \"10s\" or \"1m30s\"")
	cmd.Flags().BoolP("suppress-redundant", "", false, "suppress redundant update if the subscribed value didn't not change")
	cmd.Flags().DurationP("heartbeat-interval", "", 0, "heartbeat interval in case suppress-redundant is enabled")
	cmd.Flags().StringSliceP("model", "", []string{}, "subscribe request used model(s)")
	cmd.Flags().Bool("quiet", false, "suppress stdout printing")
	cmd.Flags().StringP("target", "", "", "subscribe request target")
	cmd.Flags().StringSliceP("name", "n", []string{}, "reference subscriptions by name, must be defined in gnmic config file")
	cmd.Flags().StringSliceP("output", "", []string{}, "reference to output groups by name, must be defined in gnmic config file")
	//
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		cli.config.BindPFlag(cmd.Name()+"-"+flag.Name, flag)
	})
}

func newSubscribeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscribe",
		Aliases: []string{"sub"},
		Short:   "subscribe to gnmi updates on targets",
		Annotations: map[string]string{
			"--path":        "XPATH",
			"--prefix":      "PREFIX",
			"--model":       "MODEL",
			"--mode":        "SUBSC_MODE",
			"--stream-mode": "STREAM_MODE",
			"--name":        "SUBSCRIPTION",
			"--output":      "OUTPUT",
		},
		SilenceUsage: true,
		RunE:         runSubscribeCmd,
		PostRun: func(cmd *cobra.Command, args []string) {
			cmd.ResetFlags()
			initSubscribeFlags(cmd)
		},
	}

	initSubscribeFlags(cmd)
	return cmd
}

func runSubscribeCmd(cmd *cobra.Command, args []string) error {
	gctx, gcancel = context.WithCancel(context.Background())
	setupCloseHandler(gcancel)
	targetsConfig, err := cli.config.GetTargets()
	if err != nil {
		return fmt.Errorf("failed getting targets config: %v", err)
	}

	subscriptionsConfig, err := cli.config.GetSubscriptions()
	if err != nil {
		return fmt.Errorf("failed getting subscriptions config: %v", err)
	}

	outs, err := cli.config.GetOutputs()
	if err != nil {
		return err
	}

	epconfig, err := cli.config.GetEventProcessors()
	if err != nil {
		return err
	}

	if cli.collector == nil {
		cfg := &collector.Config{
			PrometheusAddress:   cli.config.PrometheusAddress,
			Debug:               cli.config.Debug,
			Format:              cli.config.Format,
			TargetReceiveBuffer: cli.config.TargetBufferSize,
			RetryTimer:          cli.config.Retry,
		}

		cli.collector = collector.NewCollector(cfg, targetsConfig,
			collector.WithDialOptions(createCollectorDialOpts()),
			collector.WithSubscriptions(subscriptionsConfig),
			collector.WithOutputs(outs),
			collector.WithLogger(cli.logger),
			collector.WithEventProcessors(epconfig),
		)
	} else {
		// prompt mode
		for name, outCfg := range outs {
			cli.collector.AddOutput(name, outCfg)
		}
		for _, sc := range subscriptionsConfig {
			cli.collector.AddSubscriptionConfig(sc)
		}
		for _, tc := range targetsConfig {
			cli.collector.AddTarget(tc)
		}
	}

	cli.collector.InitOutputs(gctx)

	go cli.collector.Start(gctx)

	wg := new(sync.WaitGroup)
	wg.Add(len(cli.collector.Targets))
	for name := range cli.collector.Targets {
		go func(tn string) {
			defer wg.Done()
			tRetryTimer := cli.collector.Targets[tn].Config.RetryTimer
			for {
				err = cli.collector.Subscribe(gctx, tn)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						cli.logger.Printf("failed to initialize target '%s' timeout (%s) reached", tn, targetsConfig[tn].Timeout)
					} else {
						cli.logger.Printf("failed to initialize target '%s': %v", tn, err)
					}
					cli.logger.Printf("retrying target %s in %s", tn, tRetryTimer)
					time.Sleep(tRetryTimer)
					continue
				}
				return
			}
		}(name)
	}
	wg.Wait()
	polledTargetsSubscriptions := cli.collector.PolledSubscriptionsTargets()
	if len(polledTargetsSubscriptions) > 0 {
		pollTargets := make([]string, 0, len(polledTargetsSubscriptions))
		for t := range polledTargetsSubscriptions {
			pollTargets = append(pollTargets, t)
		}
		sort.Slice(pollTargets, func(i, j int) bool {
			return pollTargets[i] < pollTargets[j]
		})
		s := promptui.Select{
			Label:        "select target to poll",
			Items:        pollTargets,
			HideSelected: true,
		}
		waitChan := make(chan struct{}, 1)
		waitChan <- struct{}{}
		mo := &formatters.MarshalOptions{
			Multiline: true,
			Indent:    "  ",
			Format:    cli.config.Format}
		go func() {
			for {
				select {
				case <-waitChan:
					_, name, err := s.Run()
					if err != nil {
						fmt.Printf("failed selecting target to poll: %v\n", err)
						continue
					}
					ss := promptui.Select{
						Label:        "select subscription to poll",
						Items:        polledTargetsSubscriptions[name],
						HideSelected: true,
					}
					_, subName, err := ss.Run()
					if err != nil {
						fmt.Printf("failed selecting subscription to poll: %v\n", err)
						continue
					}
					response, err := cli.collector.TargetPoll(name, subName)
					if err != nil && err != io.EOF {
						fmt.Printf("target '%s', subscription '%s': poll response error:%v\n", name, subName, err)
						continue
					}
					if response == nil {
						fmt.Printf("received empty response from target '%s'\n", name)
						continue
					}
					switch rsp := response.Response.(type) {
					case *gnmi.SubscribeResponse_SyncResponse:
						fmt.Printf("received sync response '%t' from '%s'\n", rsp.SyncResponse, name)
						waitChan <- struct{}{}
						continue
					}
					b, err := mo.Marshal(response, nil)
					if err != nil {
						fmt.Printf("target '%s', subscription '%s': poll response formatting error:%v\n", name, subName, err)
						fmt.Println(string(b))
						waitChan <- struct{}{}
						continue
					}
					fmt.Println(string(b))
					waitChan <- struct{}{}
				case <-gctx.Done():
					return
				}
			}
		}()
	}

	if promptMode {
		return nil
	}
	for range gctx.Done() {
		return gctx.Err()
	}
	return nil
}
