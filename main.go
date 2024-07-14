package main

import (
	"flag"

	"github.com/chendefine/coze2openai/server"
)

const (
	defaultConfigPath = "./config.json"
)

func parseConfigPath() string {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", defaultConfigPath, "config file path")
	flag.Parse()
	return cfgPath
}

func main() {
	cfg := server.LoadConfig(parseConfigPath())
	svr, err := server.NewServer(cfg)
	if err != nil {
		panic(err)
	}
	err = svr.Run()
	if err != nil {
		panic(err)
	}
}
