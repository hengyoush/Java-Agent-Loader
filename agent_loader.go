package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const ATTACH_ERROR_BADVERSION = 101
const JNI_ENOMEM = -4
const ATTACH_ERROR_BADJAR = 100
const ATTACH_ERROR_NOTONCP = 101
const ATTACH_ERROR_STARTFAIL = 102

var conn net.Conn

func main() {
	args := os.Args[1:]
	pid, err := strconv.Atoi(args[0])
	agentFilePath := args[1]
	options := args[2]
	log.Default().Printf("pid: %s, agentFilePath: %s, options: %s\n", args[0], agentFilePath, options)
	if err != nil {
		log.Fatalf("fail to parse pid: %s, %v\n", args[0], err)
	}
	if !FileExist(agentFilePath) {
		log.Fatalf("agent jar path not exists: %s\n", agentFilePath)
	}
	socketFileName := "/proc/" + args[0] + "/root/" + "tmp/" + ".java_pid" + args[0]
	if !FileExist(socketFileName) {
		// create attach file
		attachFile, err := createAttachFile(pid)
		if err != nil {
			log.Fatalf("fail to create attach file, %v\n", err)
		}
		defer os.Remove(attachFile.Name())
		err = syscall.Kill(pid, syscall.SIGQUIT)
		if err != nil {
			log.Fatalf("fail to send quit, %v\n", err)
		}
		delayStep := 100
		attachTimeout := 3000
		timeSpend := 0
		delay := 0
		for timeSpend <= attachTimeout && !FileExist(socketFileName) {
			delay += delayStep
			time.Sleep(time.Millisecond * time.Duration(delay))
			timeSpend += delay
			if timeSpend > attachTimeout/2 && !FileExist(socketFileName) {
				syscall.Kill(pid, syscall.SIGQUIT)
			}
		}
		if !FileExist(socketFileName) {
			log.Fatalf("Unable to open socket file %s: "+
				"target process %d doesn't respond within %dms "+
				"or HotSpot VM not loaded\n", socketFileName, pid,
				timeSpend)
		}
	}
	conn, err = net.Dial("unix", socketFileName)
	if err != nil {
		log.Fatalf("fail to connect socketpath: %s, %v\n", socketFileName, err)
	}
	defer conn.Close()
	cmds := agentFilePath + "=" + options
	err = loadAgentLibrary("instrument", false, cmds)
	if err != nil {
		log.Fatalln(err)
	}
	log.Default().Println("load success!")

}

func FileExist(path string) bool {
	fs, err := os.Lstat(path)
	if err == nil {
		log.Default().Printf("file exist success: %s, fs: %v\n", path, fs)
		return true
	}
	return !os.IsNotExist(err)
}

func createAttachFile(pid int) (*os.File, error) {
	fn := ".attach_pid" + strconv.Itoa(pid)
	path := "/proc/" + strconv.Itoa(pid) + "/cwd/" + fn
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		file, err = os.OpenFile("/tmp/"+fn, os.O_RDWR|os.O_CREATE, 0755)
		if err != nil {
			return nil, err
		} else {
			log.Default().Printf("create attach pid file success2!\n")
		}
	} else {
		log.Default().Printf("create attach pid file success!\n")
	}
	return file, nil

}

func loadAgentLibrary(agentLibrary string, isAbs bool, options string) error {
	args := make([]string, 3)
	args[0] = agentLibrary
	args[1] = strconv.FormatBool(isAbs)
	args[2] = options
	err := execute("load", args)
	if err == nil {

		bytes, err := io.ReadAll(conn)
		if err != nil {
			return err
		}
		responseMessage := string(bytes)
		msgPrefix := "return code: "
		if responseMessage == "" {
			return errors.New("Target VM did not respond")
		} else if strings.HasPrefix(responseMessage, msgPrefix) {
			retCode, err := strconv.Atoi(strings.TrimSpace(responseMessage[len(msgPrefix):]))
			if err != nil {
				return errors.New(fmt.Sprintf("retCode not a valid int, but: %s, err: %v", responseMessage[len(msgPrefix):], err))
			}
			if retCode != 0 {
				switch retCode {
				case JNI_ENOMEM:
					return fmt.Errorf("insuffient memory")
				case ATTACH_ERROR_BADJAR:
					return fmt.Errorf("agent JAR not found or no Agent-Class attribute")
				case ATTACH_ERROR_NOTONCP:
					return fmt.Errorf("unable to add JAR file to system class path")
				case ATTACH_ERROR_STARTFAIL:
					return fmt.Errorf("agent JAR loaded but agent failed to initialize")
				default:
					return fmt.Errorf("failed to load agent - unknown reason: %d", retCode)
				}
			}
			return nil
		} else {
			return errors.New(fmt.Sprintf("Agent load failed, response: %s", responseMessage))
		}
	} else {
		return err
	}
}

func execute(cmd string, args []string) error {
	if len(args) > 3 {
		log.Fatalln("args length > 3")
	}

	err := writeString("1")
	if err != nil {
		goto complete
	}
	err = writeString(cmd)
	if err != nil {
		goto complete
	}
	for i := 0; i < 3; i++ {
		if i < len(args) {
			err = writeString(args[i])
		} else {
			err = writeString("")
		}
		if err != nil {
			goto complete
		}
	}

complete:
	completionStatus, err := readInt()
	if err != nil {
		return err
	}
	if completionStatus != 0 {
		errorMessage, _ := readErrorMessage()
		if completionStatus == ATTACH_ERROR_BADVERSION {
			return errors.New("Protocol mismatch with target VM")
		}
		if cmd == "load" {
			return errors.New("Failed to load agent library:" + errorMessage)
		} else {
			if errorMessage == "" {
				errorMessage = "Command failed in target VM"
			}
			return errors.New(errorMessage)
		}
	}
	return nil
}

func writeString(str string) error {
	_, err := conn.Write([]byte(str))
	if err != nil {
		return err
	}
	_, err = conn.Write(make([]byte, 1))
	if err != nil {
		return err
	}
	return nil
}

func readInt() (int, error) {
	buf := make([]byte, 1)
	str := ""
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return -1, err
		}
		if n > 0 {
			if buf[0] == '\n' {
				break
			} else {
				str = str + string(buf)
			}
		} else {
			break
		}
	}
	if len(str) == 0 {
		return -1, errors.New("Premature EOF")
	}
	value, err := strconv.Atoi(str)
	if err != nil {
		return -1, errors.New(fmt.Sprintf("Non-numeric value found - int expected, but: %s\n", str))
	}
	return value, nil
}

func readErrorMessage() (string, error) {
	bytes, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
