package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mc-go/internal/plugin"
	"mc-go/internal/server"

	_ "mc-go/plugins/anticheat"
	_ "mc-go/plugins/hello"
)

func main() {
	addr := flag.String("addr", ":25565", "TCP address to listen on")
	motd := flag.String("motd", server.MOTD, "server list description")
	maxPlayers := flag.Int("max-players", server.MaxPlayers, "status max players and login cap")
	worldPath := flag.String("world", "world.json", "block-override save file (empty disables persistence)")
	worldDir := flag.String("world-dir", "", "vanilla-format world directory (level.dat + region files); takes precedence over -world")
	serverDir := flag.String("server-dir", ".", "directory for the vanilla-format admin files (ops.json, whitelist.json, banned-*.json); empty disables them")
	levelName := flag.String("level-name", "", "level name written into level.dat (empty uses the directory name)")
	seed := flag.Int64("seed", 1, "world seed written into the generator settings")
	generateStructures := flag.Bool("generate-structures", true, "whether the world generates structures (recorded in the generator settings)")
	saveInterval := flag.Duration("save-interval", 5*time.Minute, "autosave period (0 disables)")
	frontendName := flag.String("frontend", "java", "protocol frontend to serve ("+strings.Join(server.FrontendNames(), ", ")+")")
	gameMode := flag.String("gamemode", "survival", "default game mode: survival, creative, adventure, spectator")
	rconAddr := flag.String("rcon-addr", "", "RCON listen address (default :25575 when a password is set)")
	rconPassword := flag.String("rcon-password", "", "RCON password; empty disables RCON like unset rcon.password")
	enableQuery := flag.Bool("enable-query", false, "enable the GameSpy4 query listener")
	queryAddr := flag.String("query-addr", "", "query listen address (default: the server address, like unset query.port)")
	levelType := flag.String("level-type", "", "overworld generator for a new world: flat (default), normal (vanilla terrain), amplified or large_biomes; a saved world keeps its own")
	propsPath := flag.String("properties", "", "vanilla server.properties to read (and complete with defaults, as the real server does); used automatically when <server-dir>/server.properties exists; command-line flags win")
	flag.Parse()

	s := server.New(*addr)
	s.MOTD = *motd
	s.MaxPlayers = *maxPlayers
	s.WorldPath = *worldPath
	s.WorldDir = *worldDir
	s.ServerDir = *serverDir
	s.RconAddr = *rconAddr
	s.RconPassword = *rconPassword
	s.EnableQuery = *enableQuery
	s.QueryAddr = *queryAddr
	s.LevelName = *levelName
	s.WorldSeed = *seed
	s.LevelType = *levelType
	s.GenerateStructures = *generateStructures
	switch strings.ToLower(*gameMode) {
	case "survival":
		s.GameMode = server.GameModeSurvival
	case "creative":
		s.GameMode = server.GameModeCreative
	case "adventure":
		s.GameMode = server.GameModeAdventure
	case "spectator":
		s.GameMode = server.GameModeSpectator
	default:
		log.Fatalf("unknown gamemode %q", *gameMode)
	}
	if *propsPath == "" && *serverDir != "" {
		if _, err := os.Stat(filepath.Join(*serverDir, "server.properties")); err == nil {
			*propsPath = filepath.Join(*serverDir, "server.properties")
		}
	}
	if *propsPath != "" {
		props, err := server.LoadServerProperties(*propsPath)
		if err != nil {
			log.Fatal(err)
		}
		// Flags given on the command line override their properties.
		flagKeys := map[string][]string{
			"addr": {"server-port"}, "motd": {"motd"}, "max-players": {"max-players"},
			"world": {"level-name"}, "world-dir": {"level-name"}, "level-name": {"level-name"},
			"seed": {"level-seed"}, "generate-structures": {"generate-structures"},
			"gamemode": {"gamemode"}, "rcon-addr": {"enable-rcon"}, "rcon-password": {"enable-rcon"},
			"enable-query": {"enable-query"}, "query-addr": {"enable-query"}, "level-type": {"level-type"},
		}
		skip := map[string]bool{}
		flag.Visit(func(f *flag.Flag) {
			for _, k := range flagKeys[f.Name] {
				skip[k] = true
			}
		})
		dir := *serverDir
		if dir == "" {
			dir = filepath.Dir(*propsPath)
		}
		if err := s.ApplyProperties(props, dir, skip); err != nil {
			log.Fatal(err)
		}
		*worldPath, *worldDir = s.WorldPath, s.WorldDir
	}
	if err := s.LoadWorld(); err != nil {
		log.Fatalf("load world %q: %v", *worldPath, err)
	}
	if err := s.LoadServerData(); err != nil {
		log.Fatalf("load server data: %v", err)
	}
	if err := s.LoadAdminFiles(); err != nil {
		log.Fatalf("load admin files: %v", err)
	}
	if err := s.InitPlugins(); err != nil {
		log.Fatal(err)
	}
	fe, ok := server.NewFrontend(*frontendName, s)
	if !ok {
		log.Fatalf("unknown frontend %q (have %s)", *frontendName, strings.Join(server.FrontendNames(), ", "))
	}
	if *saveInterval > 0 && (*worldPath != "" || *worldDir != "") {
		go func() {
			t := time.NewTicker(*saveInterval)
			defer t.Stop()
			for range t.C {
				if s.ShuttingDown() {
					return
				}
				if err := s.SaveWorld(); err != nil {
					log.Printf("autosave: %v", err)
				}
				if err := s.SaveServerData(); err != nil {
					log.Printf("autosave data: %v", err)
				}
			}
		}()
	}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		log.Print("shutting down, saving world")
		if err := s.SaveWorld(); err != nil {
			log.Printf("save on shutdown: %v", err)
		}
		if err := s.SaveServerData(); err != nil {
			log.Printf("save on shutdown: %v", err)
		}
		s.Shutdown("Server closed")
	}()

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	var vers []string
	for _, v := range fe.Versions() {
		vers = append(vers, v.Name)
	}
	log.Printf("mc-go listening on %s via %s frontend (versions %s, plugins %d)",
		s.Addr, fe.Name(), strings.Join(vers, ","), len(plugin.All()))
	if err := s.ServeFrontend(*frontendName, ln); err != nil {
		if s.ShuttingDown() {
			return // planned /stop or signal: clean exit
		}
		log.Fatal(err)
	}
}
