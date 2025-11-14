package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var (
	// Version information (set during build)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

const (
	upgradeInfoFile     = "upgrade-info.json"
	dockerComposeFile   = "docker-compose.yml"
	dockerComposeNext   = "docker-compose.yml-next"
	dockerComposeBackup = "docker-compose.yml-backup"
)

func main() {
	var chainFolder, dataFolder string

	var rootCmd = &cobra.Command{
		Use:     "cosmos-docker-upgrader <ChainFolder> <DataFolder>",
		Short:   "Cosmos Docker Upgrader - Watches for upgrade-info.json and manages Docker Compose upgrades",
		Version: fmt.Sprintf("%s (built: %s, commit: %s)", Version, BuildTime, GitCommit),
		Long: `Cosmos Docker Upgrader watches for upgrade-info.json files in a data directory
and automatically manages Docker Compose upgrades for Cosmos chains.

Parameters:
  <ChainFolder>: Directory containing docker-compose.yml and docker-compose.yml-next files
  <DataFolder>:  Directory to watch for upgrade-info.json file appearances

When upgrade-info.json appears:
- If docker-compose.yml-next exists: performs upgrade (down, backup, swap, up)
- If docker-compose.yml-next missing: logs the event only`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			chainFolder = args[0]
			dataFolder = args[1]
			runWatcher(chainFolder, dataFolder)
		},
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runWatcher(chainFolder, dataFolder string) {
	log.Printf("Starting Cosmos Docker Upgrader %s", Version)
	log.Printf("Chain folder: %s", chainFolder)
	log.Printf("Data folder: %s", dataFolder)

	// Validate directories exist
	if err := validateDirectories(chainFolder, dataFolder); err != nil {
		log.Fatalf("Validation failed: %v", err)
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Add data folder to watcher
	err = watcher.Add(dataFolder)
	if err != nil {
		log.Fatalf("Failed to add data folder to watcher: %v", err)
	}

	log.Printf("Watching for %s in %s", upgradeInfoFile, dataFolder)

	// Watch for file events
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				filename := filepath.Base(event.Name)
				if filename == upgradeInfoFile {
					log.Printf("Detected %s file event: %s", upgradeInfoFile, event.Op)
					handleUpgradeFile(chainFolder, dataFolder)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func validateDirectories(chainFolder, dataFolder string) error {
	// Check if chain folder exists
	if _, err := os.Stat(chainFolder); os.IsNotExist(err) {
		return fmt.Errorf("chain folder does not exist: %s", chainFolder)
	}

	// Check if data folder exists
	if _, err := os.Stat(dataFolder); os.IsNotExist(err) {
		return fmt.Errorf("data folder does not exist: %s", dataFolder)
	}

	// Check if docker-compose.yml exists in chain folder
	dockerComposePath := filepath.Join(chainFolder, dockerComposeFile)
	if _, err := os.Stat(dockerComposePath); os.IsNotExist(err) {
		return fmt.Errorf("docker-compose.yml not found in chain folder: %s", dockerComposePath)
	}

	log.Printf("Validation passed - both directories exist and docker-compose.yml found")
	return nil
}

func handleUpgradeFile(chainFolder, dataFolder string) {
	// Wait a brief moment for file to be fully written
	time.Sleep(100 * time.Millisecond)

	// Check if docker-compose.yml-next exists
	nextComposePath := filepath.Join(chainFolder, dockerComposeNext)
	if _, err := os.Stat(nextComposePath); os.IsNotExist(err) {
		log.Printf("No %s file found - upgrade skipped", dockerComposeNext)
		return
	}

	log.Printf("Found %s - proceeding with upgrade", dockerComposeNext)

	if err := performUpgrade(chainFolder); err != nil {
		log.Printf("Upgrade failed: %v", err)
		return
	}

	log.Printf("Upgrade completed successfully")
}

func performUpgrade(chainFolder string) error {
	log.Printf("Starting Docker Compose upgrade sequence")

	// Step 1: docker-compose down
	log.Printf("Step 1: Stopping containers with docker-compose down")
	if err := runCommand(chainFolder, "docker-compose", "down"); err != nil {
		return fmt.Errorf("failed to stop containers: %v", err)
	}

	// Step 2: Backup current docker-compose.yml
	currentPath := filepath.Join(chainFolder, dockerComposeFile)
	backupPath := filepath.Join(chainFolder, dockerComposeBackup)

	log.Printf("Step 2: Backing up current docker-compose.yml to docker-compose.yml-backup")
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup docker-compose.yml: %v", err)
	}

	// Step 3: Move docker-compose.yml-next to docker-compose.yml
	nextPath := filepath.Join(chainFolder, dockerComposeNext)

	log.Printf("Step 3: Promoting docker-compose.yml-next to docker-compose.yml")
	if err := os.Rename(nextPath, currentPath); err != nil {
		// Try to restore backup if this fails
		log.Printf("Failed to promote next compose file, attempting to restore backup")
		if restoreErr := os.Rename(backupPath, currentPath); restoreErr != nil {
			return fmt.Errorf("failed to promote next compose file AND failed to restore backup: %v, restore error: %v", err, restoreErr)
		}
		return fmt.Errorf("failed to promote docker-compose.yml-next: %v", err)
	}

	// Step 4: docker-compose up -d
	log.Printf("Step 4: Starting containers with docker-compose up -d")
	if err := runCommand(chainFolder, "docker-compose", "up", "-d"); err != nil {
		return fmt.Errorf("failed to start containers: %v", err)
	}

	return nil
}

func runCommand(workDir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Running command in %s: %s %v", workDir, name, args)
	return cmd.Run()
}
