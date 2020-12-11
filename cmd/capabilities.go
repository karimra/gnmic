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
	"fmt"
	"os"
	"sync"

	"github.com/karimra/gnmic/collector"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/proto/gnmi_ext"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func capabilities(ctx context.Context, tName string, wg *sync.WaitGroup, lock *sync.Mutex) {
	defer wg.Done()
	ext := make([]*gnmi_ext.Extension, 0) //
	if cli.config.PrintRequest {
		lock.Lock()
		fmt.Fprint(os.Stderr, "Capabilities Request:\n")
		err := printMsg(tName, &gnmi.CapabilityRequest{
			Extension: ext,
		})
		if err != nil {
			cli.logger.Printf("error marshaling capabilities request: %v", err)
			if !cli.config.Log {
				fmt.Printf("error marshaling capabilities request: %v", err)
			}
		}
		lock.Unlock()
	}

	cli.logger.Printf("sending gNMI CapabilityRequest: gnmi_ext.Extension='%v' to %s", ext, tName)
	response, err := cli.collector.Capabilities(ctx, tName, ext...)
	if err != nil {
		cli.logger.Printf("error sending capabilities request: %v", err)
		return
	}
	lock.Lock()
	defer lock.Unlock()
	fmt.Fprint(os.Stderr, "Capabilities Response:\n")
	err = printMsg(tName, response)
	if err != nil {
		cli.logger.Printf("error marshaling capabilities response from %s: %v", tName, err)
		if !cli.config.Log {
			fmt.Printf("error marshaling capabilities response from %s: %v\n", tName, err)
		}
	}
}

func newCapabilitiesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "capabilities",
		Aliases:      []string{"cap"},
		Short:        "query targets gnmi capabilities",
		SilenceUsage: true,
		RunE:         runCapabilities,
	}
	cmd.Flags().BoolP("version", "", false, "show gnmi version only")

	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		cli.config.BindPFlag(cmd.Name()+"-"+flag.Name, flag)
	})
	return cmd
}

func runCapabilities(cmd *cobra.Command, args []string) error {
	if cli.config.Format == "event" {
		return fmt.Errorf("format event not supported for Capabilities RPC")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setupCloseHandler(cancel)
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
	if cli.collector == nil {
		cfg := &collector.Config{
			Debug:      cli.config.Debug,
			Format:     cli.config.Format,
			RetryTimer: cli.config.Retry,
		}

		cli.collector = collector.NewCollector(cfg, targetsConfig,
			collector.WithDialOptions(createCollectorDialOpts()),
			collector.WithSubscriptions(subscriptionsConfig),
			collector.WithOutputs(outs),
			collector.WithLogger(cli.logger),
		)
	} else {
		// prompt mode
		for _, tc := range targetsConfig {
			cli.collector.AddTarget(tc)
		}
	}

	wg := new(sync.WaitGroup)
	wg.Add(len(cli.collector.Targets))
	lock := new(sync.Mutex)
	for tName := range cli.collector.Targets {
		go capabilities(ctx, tName, wg, lock)
	}
	wg.Wait()
	return nil
}
