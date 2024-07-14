package server

import (
	"errors"
	"fmt"

	coze "github.com/chendefine/go-coze"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var (
	errNoValidAccount = errors.New("no valid account")
)

type Server struct {
	bots   map[string]*coze.Bot
	models map[string][]string
	auths  map[string]struct{}

	port     int
	endpoint string
	engine   *gin.Engine
}

func NewServer(cfg *ServerConfig) (*Server, error) {
	server := &Server{
		bots:   make(map[string]*coze.Bot),
		models: make(map[string][]string),
		auths:  make(map[string]struct{}),

		port:     cfg.Port,
		endpoint: cfg.Endpoint,
	}

	for _, account := range cfg.Accounts {
		for _, bot := range account.Bots {
			log.Infof("regist bot[%s]", bot)
			server.bots[bot] = coze.NewBot(&coze.BotConfig{Host: account.Host, Token: account.Token, BotId: bot})
		}
	}
	if len(server.bots) == 0 {
		return nil, errNoValidAccount
	}

	// if not assign model, use all bots load balance
	for bot := range server.bots {
		server.models[""] = append(server.models[""], bot)
	}

	for model, bots := range cfg.Models {
		for _, bot := range bots {
			if _, ok := server.bots[bot]; ok {
				server.models[model] = append(server.models[model], bot)
			} else {
				log.Warnf("model[%s] bot[%s] is not assign", model, bot)
			}
		}
		if len(server.models[model]) == 0 {
			log.Warnf("model[%s] has none valid bot, skip", model)
			delete(server.models, model)
		} else {
			log.Infof("model[%s] bind to bot%v", model, server.models[model])
		}
	}
	if len(server.models) == 0 {
		bots := make([]string, 0, len(server.bots))
		for bot := range server.bots {
			bots = append(bots, bot)
		}
		log.Infof("any model bind to bot%v", bots)
	}

	for _, token := range cfg.Tokens {
		server.auths[token] = struct{}{}
	}

	gin.SetMode(gin.ReleaseMode)
	server.engine = gin.Default()

	return server, nil
}

func (s *Server) Run() error {
	s.engine.POST(s.endpoint, s.Completions)
	addr := fmt.Sprintf(":%d", s.port)

	log.Infof("coze2openai serve at 0.0.0.0:%d%s", s.port, s.endpoint)

	return s.engine.Run(addr)
}
