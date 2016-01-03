package main

import (
	"flag"
	"fmt"
	"time"
	"path/filepath"
	"os"
	"os/exec"
	"os/signal"
)

/* ----- */

func FatalError(msg string) {
	fmt.Println("*** Error:", msg)
	os.Exit(1)
}

/* ----- */

type strslice []string

func (s *strslice) String() string {
	return fmt.Sprintf("%s", *s)
}

func (s *strslice) Set(value string) error {
	fmt.Printf("%s\n", value)
	*s = append(*s, value)
	return nil
}

type Flags struct {
	root string
	outfile string
	dirs strslice	
}

var flags Flags

/* ----- */

type PStateErr struct {
	PState *os.ProcessState
	Err error
}

/* ----- */

type Scanner struct {
	srcs []string
	dirs []string
	mtime time.Time
}

func NewScanner(srcs []string, dirs []string) *Scanner {
	s := Scanner{}
	s.srcs = srcs
	s.dirs = dirs
	s.mtime = time.Now()
	return &s
}

func (s *Scanner) detect() bool {

	for _, f := range s.srcs {
		fi, err := os.Stat(f)
		if err == nil {
			mtime := fi.ModTime()
			if mtime.After(s.mtime) {
				s.mtime = mtime
				fmt.Printf("Changed: %s\n", f)
				return true
			}
		}
	}

	return false
}

/* ----- */

type Builder struct {
	srcs []string
	outfile string
}

func NewBuilder(outfile string, srcs []string) *Builder {
	b := Builder{}
	b.outfile = outfile
	b.srcs = srcs
	return &b
}

func (b *Builder) build() error {

	fmt.Printf("Building: %s\n", b.srcs)

	startTime := time.Now()

	args := make([]string, 0, 10)
	args = append(args, "build")
	args = append(args, "-o")
	args = append(args, b.outfile)
	args = append(args, b.srcs...)

	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()

	elapsedTime := time.Since(startTime)

	if err != nil {
		fmt.Printf("Build failed:\n%s\n", out)
	} else {
		fmt.Printf("Build done: %s\n", elapsedTime)
	}

	return err
}

/* ----- */

type Runner struct {
	outfile string
	pchan chan PStateErr
	proc *os.Process
}

func NewRunner(outfile string, pchan chan PStateErr) *Runner {
	r := Runner{}
	r.outfile = outfile
	r.pchan = pchan
	r.proc = nil
	return &r
}

func (r *Runner) spawn() error {
	fmt.Printf("Starting %s\n", r.outfile)

	argv := make([]string, 0, 10)
	argv = append(argv, r.outfile)

	attr := &os.ProcAttr{}
	attr.Files = make([]*os.File, 0, 3)
	attr.Files = append(attr.Files, os.Stdin)
	attr.Files = append(attr.Files, os.Stdout)
	attr.Files = append(attr.Files, os.Stderr)

	proc, err := os.StartProcess(r.outfile, argv, attr)
	if err != nil {
		return err
	}

	go func() {
		fmt.Printf("Waiting on %s\n", r.outfile)
		pstate, err := proc.Wait()
		r.pchan <- PStateErr{pstate, err}
	}()

	r.proc = proc
	return nil
}

func (r *Runner) kill() bool {
	if r.proc != nil {
		r.proc.Kill()
		r.proc = nil
		return true
	}
	return false
}

/* ----- */

const (
	building = iota
	running = iota
	killing = iota
	exiting = iota
)

func main() {
	flag.StringVar(&flags.root, "r", "", "Root directory for executable")
	flag.StringVar(&flags.outfile, "o", "lr-bin", "Executable file")
	flag.Var(&flags.dirs, "d", "Directory to watch")

	flag.Parse()

	srcs := flag.Args()
	if len(srcs) == 0 {
		FatalError("No source files")
	}

	outfile, err := filepath.Abs(flags.outfile)
	if err != nil {
		FatalError(err.Error())
	}

	// Channels and signals
	pchan := make(chan PStateErr)
	cchan := make(chan os.Signal, 1)
	signal.Notify(cchan, os.Interrupt, os.Kill)

	// Change scanner
	scanner := NewScanner(srcs, nil)

	// Executable builder
	builder := NewBuilder(outfile, srcs)

	// Executable runner
	runner := NewRunner(outfile, pchan)

	// Event loop
	state := building
	for (state != exiting) {
		if state == building {
			// Building
			fmt.Printf("Building...\n")

			err = builder.build()
			if err != nil {
				fmt.Println("Build failed", err)
			} else {
				runner.spawn()
			}
			state = running
		} else if (state == running || state == killing) {
			// Running or killing
			select {
			default:
				if scanner.detect() {
					if runner.kill() {
						state = killing
					} else {
						state = building
					}
				}

			case pstate := <-pchan:
				if pstate.Err != nil {
					fmt.Printf("Process exited: %s\n", pstate.Err)
				} else {
					fmt.Printf("Process exited without error\n")
				}
				if (state == killing) {
					state = building
				} else {
					state = exiting
				}

			case sig := <- cchan:
				fmt.Printf("Signal: %s\n", sig)
				state = exiting
			}

			time.Sleep(250 * time.Millisecond)
		}
	}

	fmt.Printf("Done running\n")
}
