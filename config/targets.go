package config

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/karimra/gnmic/collector"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
)

// this is needed because of https://github.com/spf13/viper/issues/819
func (c *Config) loadEmpyTargets(v *viper.Viper) {
	targets := v.GetStringMap("targets")
	for a := range targets {
		if _, ok := c.Targets[a]; !ok {
			c.Targets[a] = new(collector.TargetConfig)
		}
	}
}

func (c *Config) GetTargets() (map[string]*collector.TargetConfig, error) {
	var err error
	targets := make(map[string]*collector.TargetConfig)
	defGrpcPort := c.Port
	// case address is defined in config file
	if len(c.Address) > 0 {
		if c.Username == "" {
			c.Username, err = readUsername()
			if err != nil {
				return nil, err
			}
		}
		if c.Password == "" {
			c.Password, err = readPassword()
			if err != nil {
				return nil, err
			}
		}
		for _, addr := range c.Address {
			tc := new(collector.TargetConfig)
			if !strings.HasPrefix(addr, "unix://") {
				_, _, err := net.SplitHostPort(addr)
				if err != nil {
					if strings.Contains(err.Error(), "missing port in address") ||
						strings.Contains(err.Error(), "too many colons in address") {
						addr = net.JoinHostPort(addr, defGrpcPort)
					} else {
						c.logger.Printf("error parsing address '%s': %v", addr, err)
						return nil, fmt.Errorf("error parsing address '%s': %v", addr, err)
					}
				}
			}
			tc.Address = addr
			c.setTargetConfigDefaults(tc)
			targets[tc.Name] = tc
		}
		if c.Debug {
			c.logger.Printf("targets: %+v", targets)
		}
		return targets, nil
	}
	if len(c.Targets) == 0 {
		return nil, fmt.Errorf("no targets found")
	}
	for addr, tc := range c.Targets {
		if !strings.HasPrefix(addr, "unix://") {
			_, _, err := net.SplitHostPort(addr)
			if err != nil {
				if strings.Contains(err.Error(), "missing port in address") ||
					strings.Contains(err.Error(), "too many colons in address") {
					addr = net.JoinHostPort(addr, defGrpcPort)
				} else {
					c.logger.Printf("error parsing address '%s': %v", addr, err)
					return nil, fmt.Errorf("error parsing address '%s': %v", addr, err)
				}
			}
		}
		tc.Address = addr
		c.setTargetConfigDefaults(tc)
		if c.Debug {
			c.logger.Printf("read target config: %s", tc)
		}
		targets[tc.Name] = tc
	}
	subNames := c.SubscribeName
	if len(subNames) == 0 {
		return targets, nil
	}
	for n := range targets {
		targets[n].Subscriptions = subNames
	}
	if c.Debug {
		c.logger.Printf("targets: %+v", targets)
	}
	return targets, nil
}

func (c *Config) setTargetConfigDefaults(tc *collector.TargetConfig) {
	if tc.Name == "" {
		tc.Name = tc.Address
	}
	if tc.Username == nil {
		s := c.Username
		tc.Username = &s
	}
	if tc.Password == nil {
		s := c.Username
		tc.Password = &s
	}
	if tc.Timeout == 0 {
		tc.Timeout = c.Timeout
	}
	if tc.Insecure == nil {
		b := c.Insecure
		tc.Insecure = &b
	}
	if tc.SkipVerify == nil {
		b := c.SkipVerify
		tc.SkipVerify = &b
	}
	if tc.Insecure != nil && !*tc.Insecure {
		if tc.TLSCA == nil {
			s := c.TLSCa
			if s != "" {
				tc.TLSCA = &s
			}
		}
		if tc.TLSCert == nil {
			s := c.TLSCert
			tc.TLSCert = &s
		}
		if tc.TLSKey == nil {
			s := c.TLSKey
			tc.TLSKey = &s
		}
	}
	if tc.RetryTimer == 0 {
		tc.RetryTimer = c.Retry
	}
	if tc.TLSVersion == "" {
		tc.TLSVersion = c.TLSVersion
	}
	if tc.TLSMinVersion == "" {
		tc.TLSMinVersion = c.TLSMinVersion
	}
	if tc.TLSMaxVersion == "" {
		tc.TLSMaxVersion = c.TLSMaxVersion
	}
}

func (c *Config) GetTargetList() []*collector.TargetConfig {
	targetMap, err := c.GetTargets()
	if err != nil {
		c.logger.Printf("failed reading targets: %v", err)
		return nil
	}
	targetList := make([]*collector.TargetConfig, 0, len(targetMap))
	for _, tc := range targetMap {
		targetList = append(targetList, tc)
	}
	sort.Slice(targetList, func(i, j int) bool {
		return targetList[i].Name < targetList[j].Name
	})
	return targetList
}

func readUsername() (string, error) {
	var username string
	fmt.Print("username: ")
	_, err := fmt.Scan(&username)
	if err != nil {
		return "", err
	}
	return username, nil
}

func readPassword() (string, error) {
	fmt.Print("password: ")
	pass, err := terminal.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Println()
	return string(pass), nil
}
