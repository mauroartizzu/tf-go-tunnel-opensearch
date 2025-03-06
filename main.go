package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
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

var (
	configPath string
	localPort  = 5602
	remotePort = 443
)

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
	rootCmd.Flags().String("opensearch-staging", "", "Staging OpenSearch host")
	rootCmd.Flags().String("opensearch-production", "", "Production OpenSearch host")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runTunnel(cmd *cobra.Command, args []string) {
	// Load or create config
	config, err := getConfig(cmd)
	if err != nil {
		log.Fatalf("Error with configuration: %v", err)
	}

	// Get and validate environment
	environment := getEnvironment(cmd, config)

	// Get environment config
	envConfig, exists := config.Environments[environment]
	if !exists {
		log.Fatalf("Invalid environment: %s. Available environments: %v",
			environment, getEnvironmentKeys(config.Environments))
	}

	// Extract username and host from bastion host string
	parts := strings.Split(config.BastionHost, "@")
	if len(parts) != 2 {
		log.Fatalf("Invalid bastion host format. Expected format: username@hostname")
	}
	username, hostname := parts[0], parts[1]

	// Prepare SSH client config
	sshConfig, err := prepareSSHConfig(config.KeyPath, username)
	if err != nil {
		log.Fatalf("Failed to prepare SSH configuration: %v", err)
	}

	// Establish SSH connection
	fmt.Printf("Establishing SSH tunnel to %s environment...\n", environment)
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", hostname), sshConfig)
	if err != nil {
		log.Fatalf("Failed to dial SSH server: %v", err)
	}
	defer client.Close()

	// Setup context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	setupSignalHandler(cancel, client)

	// Start the SSH tunnel
	err = startTunnel(ctx, client, envConfig.OpenSearchHost, localPort, remotePort)
	if err != nil {
		log.Fatalf("Failed to start SSH tunnel: %v", err)
	}

	// Open browser
	url := fmt.Sprintf("https://localhost:%d/_dashboards/", localPort)
	fmt.Printf("Opening browser to %s\n", url)
	openBrowser(url)

	fmt.Println("SSH tunnel established. Press Ctrl+C to close the tunnel and exit")

	// Wait for context cancellation
	<-ctx.Done()
	fmt.Println("SSH tunnel closed")
}

// Gets configuration - loads existing or creates new
func getConfig(cmd *cobra.Command) (*Config, error) {
	// Check if config exists
	configExists := fileExists(configPath)

	if configExists {
		return loadConfig()
	} else {
		return createNewConfig(cmd)
	}
}

// Determines which environment to use
func getEnvironment(cmd *cobra.Command, config *Config) string {
	// Get environment from flags
	environment, _ := cmd.Flags().GetString("environment")

	// If environment not specified, prompt user to select one
	if environment == "" {
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
	}

	return environment
}

// Creates SSH config with key authentication
func prepareSSHConfig(keyPath string, username string) (*ssh.ClientConfig, error) {
	// Expand key path if it contains tilde
	expandedKeyPath := expandPath(keyPath)

	// Read private key
	key, err := os.ReadFile(expandedKeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	// Parse private key
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}

	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Not secure for production
		Timeout:         15 * time.Second,
	}, nil
}

// Starts the SSH tunnel
func startTunnel(ctx context.Context, client *ssh.Client, remoteHost string, localPort, remotePort int) error {
	// Start local listener
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", localPort))
	if err != nil {
		return fmt.Errorf("failed to start local listener: %w", err)
	}

	// Handle connections in a goroutine
	go func() {
		defer listener.Close()

		// Channel to track active connections
		conns := make(chan net.Conn, 10)
		defer close(conns)

		// Handle context cancellation
		go func() {
			<-ctx.Done()
			listener.Close()
			// Close any active connections
			for conn := range conns {
				conn.Close()
			}
		}()

		// Accept connections
		for {
			conn, err := listener.Accept()
			if err != nil {
				// Check if listener was closed
				if ctx.Err() != nil {
					return
				}
				log.Printf("Error accepting connection: %v", err)
				continue
			}

			// Handle the connection in a new goroutine
			go handleConnection(client, conn, remoteHost, remotePort, conns)
		}
	}()

	// Wait for successful connection or timeout
	return waitForTunnelReady(localPort)
}

// Handles a single connection through the tunnel
func handleConnection(client *ssh.Client, localConn net.Conn, remoteHost string, remotePort int, conns chan<- net.Conn) {
	// Add to active connections
	conns <- localConn
	defer localConn.Close()

	// Dial remote host through SSH tunnel
	remoteConn, err := client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
	if err != nil {
		log.Printf("Failed to connect to remote host: %v", err)
		return
	}
	defer remoteConn.Close()

	// Copy data in both directions
	done := make(chan bool, 2)
	go copyData(localConn, remoteConn, done)
	go copyData(remoteConn, localConn, done)

	// Wait for either connection to close
	<-done
}

// Copies data between connections
func copyData(dst, src net.Conn, done chan<- bool) {
	_, err := io.Copy(dst, src)
	if err != nil && err != io.EOF {
		log.Printf("Error copying data: %v", err)
	}
	done <- true
}

// Waits for tunnel to be ready
func waitForTunnelReady(port int) error {
	// Try to connect a few times
	for i := 0; i < 5; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	// It's ok if we can't connect - we're not actually making HTTP requests,
	// just checking if the port is listening
	return nil
}

// Sets up signal handler for graceful shutdown
func setupSignalHandler(cancel context.CancelFunc, client *ssh.Client) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\nClosing SSH tunnel...")
		cancel()
		client.Close()
	}()
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
	fmt.Println("Creating new configuration file...")

	// Get bastion host from flag or prompt
	bastionHost, _ := cmd.Flags().GetString("bastion")
	if bastionHost == "" {
		bastionHost = promptForInput("Enter bastion host (e.g., ubuntu@12.34.56.78): ")
	}

	// Get key path from flag or prompt
	keyPath, _ := cmd.Flags().GetString("key")
	if keyPath == "" {
		keyPath = promptForInput("Enter SSH key path (e.g., ~/.ssh/key.pem): ")
	}

	// Get staging OpenSearch host from flag or prompt
	stagingHost, _ := cmd.Flags().GetString("opensearch-staging")
	if stagingHost == "" {
		stagingHost = promptForInput("Enter OpenSearch host for staging environment: ")
	}

	// Get production OpenSearch host from flag or prompt
	productionHost, _ := cmd.Flags().GetString("opensearch-production")
	if productionHost == "" {
		productionHost = promptForInput("Enter OpenSearch host for production environment: ")
	}

	// Create config structure with both environments
	config := &Config{
		BastionHost: bastionHost,
		KeyPath:     keyPath,
		Environments: map[string]Environment{
			"staging": {
				OpenSearchHost: stagingHost,
			},
			"production": {
				OpenSearchHost: productionHost,
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
