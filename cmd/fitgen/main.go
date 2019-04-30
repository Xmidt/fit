package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tormoder/fit/cmd/fitgen/internal/profile"
)

const fitPkgImportPath = "github.com/tormoder/fit"

const (
	workbookNameXLS  = "Profile.xls"
	workbookNameXLSX = "Profile.xlsx"
)

func main() {
	sdkOverride := flag.String(
		"sdk",
		"",
		"provide or override SDK version printed in generated code",
	)
	timestamp := flag.Bool(
		"timestamp",
		false,
		"add generation timestamp to generated code",
	)
	runTests := flag.Bool(
		"test",
		false,
		"run all tests in fit repository after code has been generated",
	)
	runInstall := flag.Bool(
		"install",
		false,
		"run go install before invoking stringer (go/types related, see golang issue #11415)",
	)
	verbose := flag.Bool(
		"verbose",
		false,
		"print verbose debugging output for profile parsing and code generation",
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: fitgen [flags] [path to sdk zip, xls or xlsx file] [output directory]\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}

	l := log.New(os.Stdout, "fitgen:\t", 0)

	fitSrcDir := flag.Arg(1)
	l.Println("fit source output directory:", fitSrcDir)

	var (
		messagesOut    = filepath.Join(fitSrcDir, "messages.go")
		typesOut       = filepath.Join(fitSrcDir, "types.go")
		profileOut     = filepath.Join(fitSrcDir, "profile.go")
		typesStringOut = filepath.Join(fitSrcDir, "types_string.go")
		stringerPath   = filepath.Join(fitSrcDir, "cmd/stringer/stringer.go")
	)

	var (
		inputData []byte
		input     = flag.Arg(0)
		inputExt  = filepath.Ext(input)
		err       error
	)

	switch inputExt {
	case ".zip":
		inputData, err = readDataFromZIP(input)
	case ".xls", ".xlsx":
		inputData, err = readDataFromXLSX(input)
		if *sdkOverride == "" {
			log.Fatal("-sdk flag required if input is .xls(x)")
		}
	default:
		l.Fatalln("input file must be of type [.zip | .xls | .xlsx], got:", inputExt)
	}
	if err != nil {
		l.Fatal(err)
	}

	genOptions := []profile.GeneratorOption{
		profile.WithGenerationTimestamp(*timestamp),
		profile.WithLogger(l),
	}
	if *verbose {
		genOptions = append(genOptions, profile.WithDebugOutput())
	}

	var sdkString string
	if *sdkOverride != "" {
		sdkString = *sdkOverride
	} else {
		sdkString = parseSDKVersionStringFromZipFilePath(input)
	}

	sdkMaj, sdkMin, err := parseMajorAndMinorSDKVersion(sdkString)
	if err != nil {
		l.Fatalln("error parsing sdk version:", err)
	}

	generator, err := profile.NewGenerator(sdkMaj, sdkMin, inputData, genOptions...)
	if err != nil {
		l.Fatal(err)
	}

	fitProfile, err := generator.GenerateProfile()
	if err != nil {
		l.Fatal(err)
	}

	if err = ioutil.WriteFile(typesOut, fitProfile.TypesSource, 0644); err != nil {
		l.Fatalf("typegen: error writing types output file: %v", err)
	}

	if err = ioutil.WriteFile(messagesOut, fitProfile.MessagesSource, 0644); err != nil {
		l.Fatalf("typegen: error writing messages output file: %v", err)
	}

	if err = ioutil.WriteFile(profileOut, fitProfile.ProfileSource, 0644); err != nil {
		l.Fatalf("typegen: error writing profile output file: %v", err)
	}

	if *runInstall {
		l.Println("running go install (for go/types in stringer)")
		err = runGoInstall(fitPkgImportPath)
		if err != nil {
			l.Fatal(err)
		}
	}

	l.Println("running stringer")
	err = runStringerOnTypes(stringerPath, fitSrcDir, typesStringOut, fitProfile.StringerInput)
	if err != nil {
		l.Fatal(err)
	}
	l.Println("stringer: types done")

	logMesgNumVsMessages(fitProfile.MesgNumsWithoutMessage, l)

	if *runTests {
		err = runAllTests(fitPkgImportPath)
		if err != nil {
			l.Fatal(err)
		}
		l.Println("go test: pass")
	}

	l.Println("done")
}

func runGoInstall(pkgDir string) error {
	listCmd := exec.Command("go", "install", pkgDir+"/...")
	output, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go install: fail: %v\n%s", err, output)
	}
	return nil
}

func runStringerOnTypes(stringerPath, fitSrcDir, typesStringOut, fitTypes string) error {
	tmpFile, err := ioutil.TempFile("", "stringer")
	if err != nil {
		return fmt.Errorf("stringer: error getting temporary file: %v", err)
	}
	defer func() {
		_ = tmpFile.Close()
		err = os.Remove(tmpFile.Name())
		if err != nil {
			fmt.Printf("stringer: error removing temporary file: %v\n", err)
		}
	}()

	stringerCmd := exec.Command(
		"go",
		"run",
		stringerPath,
		"-trimprefix",
		"-type", fitTypes,
		"-output",
		tmpFile.Name(),
		fitSrcDir,
	)

	output, err := stringerCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stringer: error running on types: %v\n%s", err, output)
	}

	outFile, err := os.Create(typesStringOut)
	if err != nil {
		return fmt.Errorf("stringer: error creating output file: %v", err)
	}
	defer func() { _ = outFile.Close() }()

	_, err = outFile.WriteString("//lint:file-ignore SA4003 Ignore checks of unsigned types >= 0. stringer generates these.\n")
	if err != nil {
		return fmt.Errorf("stringer: error creating output file: %v", err)
	}

	_, err = tmpFile.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("stringer: error seeking output file: %v", err)
	}

	_, err = io.Copy(outFile, tmpFile)
	if err != nil {
		return fmt.Errorf("stringer: error creating output file: %v", err)
	}

	return nil
}

func runAllTests(pkgDir string) error {
	listCmd := exec.Command("go", "list", pkgDir+"/...")
	output, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go list: fail: %v\n%s", err, output)
	}

	splitted := strings.Split(string(output), "\n")
	var goTestArgs []string
	// Command
	goTestArgs = append(goTestArgs, "test")
	// Packages
	for _, s := range splitted {
		if strings.Contains(s, "/vendor/") {
			continue
		}
		goTestArgs = append(goTestArgs, s)
	}

	testCmd := exec.Command("go", goTestArgs...)
	output, err = testCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go test: fail: %v\n%s", err, output)
	}

	return nil
}

func logMesgNumVsMessages(msgs []string, l *log.Logger) {
	if len(msgs) == 0 {
		return
	}
	l.Println("mesgnum-vs-msgs: implementation detail below, this may be automated in the future")
	l.Println("mesgnum-vs-msgs: #mesgnum values != #generated messages, diff:", len(msgs))
	l.Println("mesgnum-vs-msgs: remember to add/verify map entries for sdk in sdk.go for the following message(s):")
	for _, msg := range msgs {
		l.Printf("mesgnum-vs-msgs: ----> mesgnum %q has no corresponding message\n", msg)
	}
}

func readDataFromZIP(path string) ([]byte, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("error opening sdk zip file: %v", err)
	}
	defer r.Close()

	var wfile *zip.File
	for _, f := range r.File {
		if f.Name == workbookNameXLS {
			wfile = f
			break
		}
		if f.Name == workbookNameXLSX {
			wfile = f
			break
		}
	}
	if wfile == nil {
		return nil, fmt.Errorf(
			"no file named %q or %q found in zip archive",
			workbookNameXLS, workbookNameXLSX,
		)
	}

	rc, err := wfile.Open()
	if err != nil {
		return nil, fmt.Errorf("error opening zip archive: %v", err)
	}
	defer rc.Close()

	data, err := ioutil.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("error reading %q from archive: %v", wfile.Name, err)
	}

	return data, nil
}

func readDataFromXLSX(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

func parseSDKVersionStringFromZipFilePath(path string) string {
	_, file := filepath.Split(path)
	ver := strings.TrimSuffix(file, ".zip")
	return strings.TrimPrefix(ver, "FitSDKRelease_")
}

func parseMajorAndMinorSDKVersion(sdkString string) (int, int, error) {
	splitted := strings.Split(sdkString, ".")
	if len(splitted) < 2 {
		return 0, 0, fmt.Errorf("could not parse major/minor version from input: %q", sdkString)
	}

	maj, err := strconv.Atoi(splitted[0])
	if err != nil {
		return 0, 0, fmt.Errorf("could not parse major version from input: %q", splitted[0])
	}

	min, err := strconv.Atoi(splitted[1])
	if err != nil {
		return 0, 0, fmt.Errorf("could not parse minor version from input: %q", splitted[1])
	}

	return maj, min, nil
}
