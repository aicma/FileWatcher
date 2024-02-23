package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/gen2brain/beeep"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"gopkg.in/ini.v1"
)

type Config struct {
	FolderToWatch      string
	SftpServer         string
	SftpUser           string
	SftpPassword       string
	PrivateKeyPath     string
	WatchExtensions    []string
	destionationFolder string
	processedFolder    string
}

func main() {

	// Read private key file
	// Create a new SSH signer
	// Create SSH client config
	// Connect to the SFTP server
	// Create SFTP client
	// Process existing files in the folder
	// Create a new file watcher
	// Start watching the specified folder without subfolders
	config, sftpClient, sshClient, watcher, shouldReturn := initialize()
	if shouldReturn {
		return
	}
	defer watcher.Close()
	defer sftpClient.Close()
	defer sshClient.Close()

	// Process file events
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {

				if hasExtension(event.Name, config.WatchExtensions) {
					// A new file was created
					fmt.Println("New file detected:", event.Name)

					// Open the file
					file, err := os.Open(event.Name)
					if err != nil {
						fmt.Println("Failed to open file:", err)
						continue
					}
					defer file.Close()

					err = copyFileToSftp(file, sftpClient, config.destionationFolder)
					if err != nil {
						fmt.Println("Error copying file to SFTP server:", err)
						continue
					}

					// Check if the "processed" folder exists
					if _, err := os.Stat(config.processedFolder); os.IsNotExist(err) {
						// Create the "processed" folder
						err := os.Mkdir(config.processedFolder, 0755)
						if err != nil {
							fmt.Println("Failed to create 'processed' folder:", err)
							continue
						}
					}

					processedFilePath := filepath.Join(config.processedFolder, filepath.Base(event.Name))
					err = moveFileToProcessed(event.Name, file, processedFilePath)
					if err != nil {
						fmt.Println("Error moving file to 'processed' folder:", err)
						continue
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			beeep.Alert("Error", "File watcher error: "+err.Error(), "error")
		}
	}
}

func initialize() (*Config, *sftp.Client, *ssh.Client, *fsnotify.Watcher, bool) {
	workDir, err := os.Getwd()
	if err != nil {
		beeep.Alert("Error", "Failed to get working directory: "+err.Error(), "error")
		return nil, nil, nil, nil, true
	}

	config, err := loadConfig(filepath.Join(workDir, "config.ini"))
	if err != nil {
		beeep.Alert("Error", "Failed to load configuration: "+err.Error(), "error")
	}
	var auth []ssh.AuthMethod
	var user string
	if config.PrivateKeyPath != "" {
		privateKey, err := os.ReadFile(config.PrivateKeyPath)
		if err != nil {
			beeep.Alert("Error", "Failed to read private key: "+config.PrivateKeyPath+" - "+err.Error(), "error")
			return nil, nil, nil, nil, true
		}

		signer, err := ssh.ParsePrivateKey(privateKey)
		if err != nil {
			beeep.Alert("Error", "Failed to parse private key: "+err.Error(), "error")
			return nil, nil, nil, nil, true
		}
		auth = []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		}
		user = config.SftpUser
	} else {
		auth = []ssh.AuthMethod{
			ssh.Password(config.SftpPassword),
		}
		user = config.SftpUser
	}
	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	sshClient, err := ssh.Dial("tcp", config.SftpServer+":22", sshConfig)
	if err != nil {
		beeep.Alert("Error", "Failed to connect to SFTP server: "+err.Error(), "error")
		return nil, nil, nil, nil, true
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		beeep.Alert("Error", "Failed to create SFTP client: "+err.Error(), "error")
		return nil, nil, nil, nil, true
	}

	err = processExistingFiles(config.FolderToWatch, sftpClient, *config)
	if err != nil {
		beeep.Alert("Error", "Failed to process existing files: "+err.Error(), "error")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		beeep.Alert("Error", "Failed to create file watcher: "+err.Error(), "error")
		return nil, nil, nil, nil, true
	}

	err = watcher.Add(config.FolderToWatch)
	if err != nil {
		beeep.Alert("Error", "Failed to watch folder: "+err.Error(), "error")
		return nil, nil, nil, nil, true
	}

	fmt.Println("Watching " + config.FolderToWatch + " folder for new files...")

	return config, sftpClient, sshClient, watcher, false
}

func processExistingFiles(folderToWatch string, sftpClient *sftp.Client, config Config) error {
	// Process existing files in the folder
	files, err := os.ReadDir(folderToWatch)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, fileInfo := range files {
		if !fileInfo.IsDir() && hasExtension(fileInfo.Name(), config.WatchExtensions) {
			// Open the file
			file, err := os.Open(filepath.Join(folderToWatch, fileInfo.Name()))
			if err != nil {
				beeep.Alert("Error", fmt.Sprintf("Failed to open file: %s", err.Error()), "error")
				continue
			}
			defer file.Close()

			err = copyFileToSftp(file, sftpClient, config.destionationFolder)
			if err != nil {
				log.Println("Error copying file to SFTP server:", err)
			}

			err = moveFileToProcessed(filepath.Join(folderToWatch, fileInfo.Name()), file, filepath.Join(config.processedFolder, fileInfo.Name()))
			if err != nil {
				log.Println("Error moving file to 'processed' folder:", err)
			}
		}
	}

	return nil
}

func copyFileToSftp(file *os.File, sftpClient *sftp.Client, destFolder string) error {
	fmt.Println("creating remote file: " + destFolder + filepath.Base(file.Name()))
	// Create remote file
	remoteFile, err := sftpClient.Create(destFolder + filepath.Base(file.Name()))
	if err != nil {
		fmt.Println("Failed to create remote file:", err)
		return err
	}

	// Copy the contents of the local file to the remote file
	_, err = io.Copy(remoteFile, file)
	if err != nil {
		fmt.Println("Failed to upload file to SFTP server:", err)
		return err
	}

	fmt.Println("File uploaded successfully")
	return nil

}

func moveFileToProcessed(srcFilePath string, file *os.File, processedPath string) error {

	// Create the destination file
	dstFile, err := os.Create(processedPath)
	if err != nil {
		fmt.Println("Failed to create destination file:", err)
		return err
	}
	defer dstFile.Close()

	// Copy the contents of the source file to the destination file
	_, err = io.Copy(dstFile, file)
	if err != nil {
		fmt.Println("Failed to copy file to 'processed' folder:", err)
		return err
	}

	fmt.Println("File copied to 'processed' folder successfully")
	err = os.Remove(srcFilePath) // delete sourceFile
	if err != nil {
		fmt.Println("Failed to delete source file:", err)
		return err
	}
	return nil
}

func loadConfig(filename string) (*Config, error) {
	cfg, err := ini.Load(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	config := &Config{}

	// Read values from the ini file
	config.FolderToWatch = cfg.Section("paths").Key("FolderToWatch").String()
	config.SftpServer = cfg.Section("server").Key("SftpServer").String()
	config.SftpUser = cfg.Section("server").Key("SftpUser").String()
	config.SftpPassword = cfg.Section("server").Key("SftpPassword").String()
	config.PrivateKeyPath = cfg.Section("paths").Key("PrivateKeyPath").String()
	config.destionationFolder = cfg.Section("server").Key("DestinationFolder").String()
	config.processedFolder = filepath.Join(config.FolderToWatch, "processed")

	// Read list of file extensions to watch
	config.WatchExtensions = cfg.Section("general").Key("WatchFileExtension").Strings(",")

	return config, nil
}

func hasExtension(filename string, extensions []string) bool {
	ext := filepath.Ext(filename)
	for _, e := range extensions {
		if e == ext {
			return true
		}
	}
	return false
}
