package plugin

import (
	"fmt"
	"strings"
	
	"github.com/mc-go/server/pkg/player"
)

type ExamplePlugin struct {
	*BasePlugin
}

func NewExamplePlugin() Plugin {
	return &ExamplePlugin{
		BasePlugin: NewBasePlugin(
			"ExamplePlugin",
			"1.0.0",
			"MC-GO Team",
			"Example plugin with basic commands",
		),
	}
}

func (p *ExamplePlugin) OnEnable() error {
	fmt.Println("[ExamplePlugin] Plugin enabled - Commands registered: help, ping, gamemode, tp, list")
	return nil
}

func (p *ExamplePlugin) OnDisable() error {
	fmt.Println("[ExamplePlugin] Plugin disabled")
	return nil
}

func (p *ExamplePlugin) RegisterCommands() map[string]CommandHandler {
	commands := make(map[string]CommandHandler)
	
	// Help command
	commands["help"] = func(sender *player.Player, args []string) error {
		sender.SendMessage("§6===== Server Commands =====")
		sender.SendMessage("§e/help §7- Show this help message")
		sender.SendMessage("§e/ping §7- Test server response")
		sender.SendMessage("§e/gamemode <mode> §7- Change game mode")
		sender.SendMessage("§e/tp <x> <y> <z> §7- Teleport to coordinates")
		sender.SendMessage("§e/list §7- List online players")
		return nil
	}
	
	// Ping command
	commands["ping"] = func(sender *player.Player, args []string) error {
		sender.SendMessage("§aPong!")
		return nil
	}
	
	// Gamemode command
	commands["gamemode"] = func(sender *player.Player, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("Usage: /gamemode <survival|creative|adventure|spectator>")
		}
		
		var mode int
		switch strings.ToLower(args[0]) {
		case "survival", "0", "s":
			mode = 0
			sender.SetGameMode(mode)
			sender.SendMessage("§aGamemode changed to Survival")
		case "creative", "1", "c":
			mode = 1
			sender.SetGameMode(mode)
			sender.SendMessage("§aGamemode changed to Creative")
		case "adventure", "2", "a":
			mode = 2
			sender.SetGameMode(mode)
			sender.SendMessage("§aGamemode changed to Adventure")
		case "spectator", "3", "sp":
			mode = 3
			sender.SetGameMode(mode)
			sender.SendMessage("§aGamemode changed to Spectator")
		default:
			return fmt.Errorf("Unknown gamemode: %s", args[0])
		}
		
		return nil
	}
	
	// Teleport command
	commands["tp"] = func(sender *player.Player, args []string) error {
		if len(args) < 3 {
			return fmt.Errorf("Usage: /tp <x> <y> <z>")
		}
		
		var x, y, z float64
		if _, err := fmt.Sscanf(args[0], "%f", &x); err != nil {
			return fmt.Errorf("Invalid X coordinate: %s", args[0])
		}
		if _, err := fmt.Sscanf(args[1], "%f", &y); err != nil {
			return fmt.Errorf("Invalid Y coordinate: %s", args[1])
		}
		if _, err := fmt.Sscanf(args[2], "%f", &z); err != nil {
			return fmt.Errorf("Invalid Z coordinate: %s", args[2])
		}
		
		sender.Teleport(x, y, z)
		sender.SendMessage(fmt.Sprintf("§aTeleported to %.1f, %.1f, %.1f", x, y, z))
		return nil
	}
	
	// List players command
	commands["list"] = func(sender *player.Player, args []string) error {
		// This would need access to the player manager
		sender.SendMessage("§6Online players: §7(feature needs player manager access)")
		return nil
	}
	
	return commands
}

func (p *ExamplePlugin) RegisterEventHandlers() map[EventType]EventHandler {
	handlers := make(map[EventType]EventHandler)
	
	// Welcome message on player join
	handlers[EventPlayerJoin] = func(event Event) error {
		if p, ok := event.Data().(*player.Player); ok {
			p.SendMessage(fmt.Sprintf("§e[Server] §aWelcome to the server, §b%s§a!", p.GetUsername()))
			p.SendMessage("§7Type §e/help §7for a list of commands")
		}
		return nil
	}
	
	return handlers
}