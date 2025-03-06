package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

type Config struct {
	BastionHost  string                 `mapstructure:"bastion_host" yaml:"bastion_host"`
	KeyPath      string                 `mapstructure:"key_path" yaml:"key_path"`
	Environments map[string]Environment `mapstructure:"environments" yaml:"environments"`
}

type Environment struct {
	OpenSearchHost string `mapstructure:"opensearch_host" yaml:"opensearch_host"`
}

var configPath string

func main() {
	// Set config file location
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error finding home directory: %v", err)
	}
	configPath = filepath.Join(home, ".config", "tf", "opensearch.yaml")

	var rootCmd = &cobra.Command{
		Use:   "opensearch-tunnel",
		Short: "Create SSH tunnel to OpenSearch and open dashboard",
		Run:   runTunnel,
	}

	rootCmd.Flags().StringP("environment", "e", "", "Environment to connect to (e.g., staging, production)")
	rootCmd.Flags().String("bastion", "", "Bastion host (e.g., ubuntu@12.34.56.78)")
	rootCmd.Flags().String("key", "", "SSH key path (e.g., ~/.ssh/key.pem)")
	rootCmd.Flags().String("opensearch", "", "OpenSearch host")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runTunnel(cmd *cobra.Command, args []string) {
	// Check if config exists
	configExists := fileExists(configPath)

	var config *Config
	var err error

	// Handle config based on its existence
	if configExists {
		// Load existing config
		config, err = loadConfig()
		if err != nil {
			log.Fatalf("Error loading config: %v", err)
		}
	} else {
		// Get parameters for creating a new config
		config, err = createNewConfig(cmd)
		if err != nil {
			log.Fatalf("Error creating config: %v", err)
		}
	}

	// Get environment from flags or prompt
	envFlag, _ := cmd.Flags().GetString("environment")
	environment := envFlag

	if environment == "" && configExists {
		// Prompt for environment selection
		environments := getEnvironmentKeys(config.Environments)
		if len(environments) == 0 {
			log.Fatalf("No environments found in config file. Please recreate your config or specify parameters.")
		}
		
		fmt.Println("Available environments:")
		for i, env := range environments {
			fmt.Printf("%d. %s\n", i+1, env)
		}
		
		fmt.Print("Select environment (enter number): ")
		var choice int
		fmt.Scanln(&choice)
		
		if choice < 1 || choice > len(environments) {
			log.Fatalf("Invalid selection")
		}
		environment = environments[choice-1]
	} else if environment == "" && !configExists {
		log.Fatalf("Environment must be specified when creating a new configuration")
	}

	// Validate environment
	envConfig, exists := config.Environments[environment]
	if !exists {
		log.Fatalf("Invalid environment: %s. Available environments: %v", 
			environment, getEnvironmentKeys(config.Environments))
	}

	// Expand key path if it contains tilde
	keyPath := expandPath(config.KeyPath)

	// Prepare SSH command
	sshCmd := exec.Command("ssh",
		"-i", keyPath,
		"-L", fmt.Sprintf("5602:%s:443", envConfig.OpenSearchHost),
		config.BastionHost,
		"-N")

	// Start SSH tunnel in background
	fmt.Printf("Establishing SSH tunnel to %s environment...\n", environment)
	if err := sshCmd.Start(); err != nil {
		log.Fatalf("Failed to start SSH tunnel: %v", err)
	}

	// Give the tunnel a moment to establish
	time.Sleep(2 * time.Second)
	fmt.Println("SSH tunnel established successfully")

	// Open browser
	url := "https://localhost:5602/_dashboards/"
	fmt.Printf("Opening browser to %s\n", url)
	openBrowser(url)

	fmt.Println("Press Ctrl+C to close the tunnel and exit")
	
	// Wait for SSH command to finish (or be interrupted)
	if err := sshCmd.Wait(); err != nil {
		fmt.Println("SSH tunnel closed")
	}
}

func loadConfig() (*Config, error) {
	// Initialize viper
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Parse config into struct
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	return &config, nil
}

func createNewConfig(cmd *cobra.Command) (*Config, error) {
	// Get values from flags or prompt
	environment, _ := cmd.Flags().GetString("environment")
	if environment == "" {
		return nil, fmt.Errorf("environment flag is required for initial configuration")
	}

	bastionHost, _ := cmd.Flags().GetString("bastion")
	if bastionHost == "" {
		bastionHost = promptForInput("Enter bastion host (e.g., ubuntu@12.34.56.78): ")
	}

	keyPath, _ := cmd.Flags().GetString("key")
	if keyPath == "" {
		keyPath = promptForInput("Enter SSH key path (e.g., ~/.ssh/key.pem): ")
	}

	opensearchHost, _ := cmd.Flags().GetString("opensearch")
	if opensearchHost == "" {
		opensearchHost = promptForInput(fmt.Sprintf("Enter OpenSearch host for %s environment: ", environment))
	}

	// Create config structure
	config := &Config{
		BastionHost: bastionHost,
		KeyPath:     keyPath,
		Environments: map[string]Environment{
			environment: {
				OpenSearchHost: opensearchHost,
			},
		},
	}

	// Create directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("error creating config directory: %w", err)
	}

	// Save config to file
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("error marshaling config: %w", err)
	}

	if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
		return nil, fmt.Errorf("error writing config file: %w", err)
	}

	fmt.Printf("Configuration saved to %s\n", configPath)
	return config, nil
}

func promptForInput(prompt string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(prompt)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}

func expandPath(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Error expanding home directory: %v", err)
		return path
	}

	return filepath.Join(home, path[1:])
}

func getEnvironmentKeys(environments map[string]Environment) []string {
	keys := make([]string, 0, len(environments))
	for k := range environments {
		keys = append(keys, k)
	}
	return keys
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// openBrowser opens the specified URL in the default browser
func openBrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		log.Printf("Failed to open browser: %v", err)
		fmt.Printf("Please manually open: %s\n", url)
	}
}
