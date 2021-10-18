package discordBot

import (
  "log"
  "os/exec"
  "os"
  "bufio"
  "strconv"
  "strings"
)

func GetSizeOfPackets(filename string) []int {
  outputFilename := "out.xml"
  args := []string{filename, "cues", "0:"+outputFilename}
  cmd := exec.Command("mkvextract", args...)
  err := cmd.Run()
  if err != nil {
    log.Println("mkvextract error")
    panic(err)
  }
  file, err := os.Open(outputFilename)
  if err != nil {
    log.Println("Cannot open ", outputFilename)
    panic(err)
  }
  defer file.Close()
  scanner := bufio.NewScanner(file)
  sizeArray := make([]int, 0, 10)
  for scanner.Scan() {
    size, err := strconv.Atoi(extractSize(scanner.Text()))
    if err != nil {
      log.Println("Cannot parse string to int while reading size of packets")
      panic(err)
    }
    sizeArray = append(sizeArray, size)
  }
  return sizeArray
}

func extractSize(s string) string {
  arr := strings.Split(s, " ")
  size := arr[2]
  return strings.Split(size, "=")[1]
}
