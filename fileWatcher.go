package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func main() {
	// Define the folder to watch and the SFTP server details
	homedir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Failed to get user home directory:", err)
		return
	}

	folderToWatch := filepath.Join(homedir, "AlpineGlow_sync")
	watchFileExtension := ".cmf"
	// Define the path to the "processed" folder
	processedPath := filepath.Join(folderToWatch, "processed")

	sftpServer := "bianca.uberspace.de"
	sftpUser := "amf"
	privateKeyPath := filepath.Join(homedir, ".ssh", "id_rsa")

	destionationFolder := "AlpineGlow/Incoming/"

	// Read private key file
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		fmt.Println("Failed to read private key:", err)
		return
	}

	// Create a new SSH signer
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		fmt.Println("Failed to parse private key:", err)
		return
	}

	// Create SSH client config
	sshConfig := &ssh.ClientConfig{
		User: sftpUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Connect to the SFTP server
	sshClient, err := ssh.Dial("tcp", sftpServer+":22", sshConfig)
	if err != nil {
		fmt.Println("Failed to connect to SSH server:", err)
		return
	}
	defer sshClient.Close()

	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		fmt.Println("Failed to create SFTP client:", err)
		return
	}
	defer sftpClient.Close()

	// Process existing files in the folder
	err = processExistingFiles(folderToWatch, sftpClient, watchFileExtension)
	if err != nil {
		fmt.Println("Error processing existing files:", err)
	}

	// Create a new file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Println("Failed to create file watcher:", err)
		return
	}
	defer watcher.Close()

	// Start watching the specified folder without subfolders
	err = watcher.Add(folderToWatch)
	if err != nil {
		fmt.Println("Failed to watch folder:", err)
		return
	}
	fmt.Println("Watching " + folderToWatch + " folder for new files...")

	// Process file events
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {

				if filepath.Ext(event.Name) == watchFileExtension {
					// A new file was created
					fmt.Println("New file detected:", event.Name)

					// Open the file
					file, err := os.Open(event.Name)
					if err != nil {
						fmt.Println("Failed to open file:", err)
						continue
					}
					defer file.Close()

					// Create remote file
					remoteFile, err := sftpClient.Create(destionationFolder + filepath.Base(event.Name))
					if err != nil {
						fmt.Println("Failed to create remote file:", err)
						continue
					}
					fmt.Println("Copying from " + file.Name() + " to " + remoteFile.Name())
					// Copy the contents of the local file to the remote file
					_, err = io.Copy(remoteFile, file)
					if err != nil {
						fmt.Println("Failed to upload file to SFTP server:", err)
						continue
					}

					fmt.Println("File uploaded successfully")

					// Check if the "processed" folder exists
					if _, err := os.Stat(processedPath); os.IsNotExist(err) {
						// Create the "processed" folder
						err := os.Mkdir(processedPath, 0755)
						if err != nil {
							fmt.Println("Failed to create 'processed' folder:", err)
							continue
						}
					}

					processedFilePath := filepath.Join(processedPath, filepath.Base(event.Name))
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
			fmt.Println("Error watching folder:", err)
		}
	}
}

func processExistingFiles(folderToWatch string, sftpClient *sftp.Client, watchFileExtension string) error {
	// Process existing files in the folder
	files, err := os.ReadDir(folderToWatch)
	if err != nil {
		return err
	}

	for _, fileInfo := range files {
		if !fileInfo.IsDir() && filepath.Ext(fileInfo.Name()) == watchFileExtension {
			// Open the file
			file, err := os.Open(filepath.Join(folderToWatch, fileInfo.Name()))
			if err != nil {
				fmt.Println("Failed to open file:", err)
				continue
			}
			defer file.Close()

			// Create remote file
			remoteFile, err := sftpClient.Create(fileInfo.Name())
			if err != nil {
				fmt.Println("Failed to create remote file:", err)
				continue
			}

			// Copy the contents of the local file to the remote file
			_, err = io.Copy(remoteFile, file)
			if err != nil {
				fmt.Println("Failed to upload file to SFTP server:", err)
				continue
			}

			fmt.Println("Processed existing file:", fileInfo.Name())
		}
	}

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
