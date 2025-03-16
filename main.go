package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	thresholdMB = 1000             // Alert if app exceeds 1.5GB RAM
	interval    = 10 * time.Second // Check interval
)

type ProcessToKill struct {
	ID string
	// MaxMemoryMB int
}
type Process struct {
	PIDs         []int
	Cmdline      string
	Comm         string
	usedMemoryMB int
}

func main() {
	for {
		checkMemoryUsage()
		time.Sleep(interval)
	}
}

func checkMemoryUsage() {
	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		fmt.Println("Error reading /proc:", err)
		return
	}
	processesToKill := []ProcessToKill{
		{
			ID: "/usr/lib64/firefox/firefox",
			// MaxMemoryMB: 1000,
		},
	}

	var processes []Process
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid := entry.Name()
		pidInt, err := strconv.Atoi(pid)
		if err != nil { // Ensure it's a numeric PID
			continue
		}

		cmdlineBytes, err := os.ReadFile(filepath.Join(procDir, pid, "cmdline"))
		if err != nil || len(cmdlineBytes) == 0 {
			continue
		}

		commBytes, err := os.ReadFile(filepath.Join(procDir, pid, "comm"))
		if err != nil || len(commBytes) == 0 {
			continue
		}

		comm := strings.Trim(string(commBytes), "\n")
		cmdline := strings.Split(string(cmdlineBytes), "\x00")[0]

		memory, err := getPSSMemory(pid)
		if err != nil {
			continue
		}
		existingProcessIndex := findInSlice(&processes, func(p Process) bool {
			return p.Cmdline == cmdline
		})
		if existingProcessIndex == -1 {
			processes = append(processes, Process{
				PIDs:         []int{pidInt},
				Comm:         comm,
				Cmdline:      cmdline,
				usedMemoryMB: memory / 1024,
			})
		} else {
			process := &processes[existingProcessIndex]
			process.usedMemoryMB += memory / 1024
			process.PIDs = append(process.PIDs, pidInt)
		}
	}

	for _, p := range processes {
		if p.usedMemoryMB > thresholdMB {
			fmt.Printf("ALERT: %s is using %d MB RAM!\n", p.Cmdline, p.usedMemoryMB)
			message := fmt.Sprintf("Application %v exceeded threshold %v. It consumes %v", p.Cmdline, thresholdMB, p.usedMemoryMB)
			SendNotification("System Monitor", message)
			isProcessToBeKilled := findInSlice(&processesToKill, func(element ProcessToKill) bool {
				return element.ID == p.Cmdline
			})
			if isProcessToBeKilled != -1 {
				killErr := KillProcessByPid(p.PIDs[0])
				if killErr != nil {
					fmt.Printf("killErr: %v\n", killErr)
				}
			}
		}
	}
	fmt.Println("-----------------------")
}

// getPSSMemory returns the Proportional Set Size (PSS) memory usage
func getPSSMemory(pid string) (int, error) {
	smapsRollupFile := filepath.Join("/proc", pid, "smaps_rollup")
	data, err := os.ReadFile(smapsRollupFile)
	if err != nil {
		return 0, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Pss:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("unexpected PSS format")
			}
			pss, err := strconv.Atoi(fields[1]) // PSS value in KB
			if err != nil {
				return 0, err
			}
			return pss, nil
		}
	}
	return 0, fmt.Errorf("PSS not found")
}

// getAggregatedMemory returns the total PSS memory used by a process and its children
func getAggregatedMemory(pid string) (int, error) {
	totalMemory := 0

	// Get PSS memory usage of the given process
	mem, err := getPSSMemory(pid)
	if err == nil {
		totalMemory += mem
	}

	// Get list of child PIDs from /proc/[pid]/task/[tid]/children
	childrenFile := filepath.Join("/proc", pid, "task", pid, "children")
	data, err := os.ReadFile(childrenFile)
	if err == nil && len(data) > 0 {
		childPIDs := strings.Fields(string(data)) // Get child PIDs
		for _, childPID := range childPIDs {
			childMem, err := getAggregatedMemory(childPID) // Recursively sum child process memory
			if err == nil {
				totalMemory += childMem
			}
		}
	}

	return totalMemory, nil
}

// returns the index of the first element that matches `match` function, or -1 otherwise
func findInSlice[T any](s *[]T, match func(T) bool) int {
	if s == nil {
		return -1
	}
	slc := *(s)
	for i, element := range slc {
		result := match(element)
		if result == true {
			return i
		}
	}
	return -1
}

func KillProcessByPid(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if err != nil {
		return fmt.Errorf("failed to kill PID %d: %v", pid, err)
	}
	fmt.Printf("Successfully killed PID %d\n", pid)
	return nil
}

func killProcess(p Process) *error {
	for _, pid := range p.PIDs {
		KillProcessByPid(pid)
	}
	return nil
}

func SendNotification(summary, body string) error {
	cmd := exec.Command("notify-send", summary, body)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to send notification: %v", err)
	}
	return nil
}
