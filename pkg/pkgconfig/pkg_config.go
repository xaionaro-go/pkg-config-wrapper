package pkgconfig

import (
	"fmt"
	"strings"

	"github.com/IGLOU-EU/go-wildcard"
)

const (
	pkgConfig = `pkg-config`
)

type libLinkType uint

const (
	libLinkTypeAuto = libLinkType(iota)
	libLinkTypeDynamic
	libLinkTypeStatic
)

type PkgConfig struct {
	Config
}

func NewPkgConfig(
	opts ...Option,
) *PkgConfig {
	return &PkgConfig{
		Config: Options(opts).Config(),
	}
}

func (p *PkgConfig) Run(args ...string) ([]string, string, int, error) {
	// TODO: split this function

	isLibLink := false
	for _, arg := range args {
		switch arg {
		case "--libs", "--libs-only-l":
			isLibLink = true
		}
	}

	if !isLibLink {
		// not about linking, so we just passing-through as is
		return p.runPkgConfig(args...)
	}

	if len(p.ForceDynamicLinkPatterns) == 0 && len(p.ForceStaticLinkPatterns) == 0 {
		// is about linking, but we do not have any rules about linking, so
		// just passing-through as is
		return p.runPkgConfig(args...)
	}

	var flags []string
	var libs []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			libs = append(libs, arg)
		}
	}

	var autoLibs []string
	var staticLibs []string
	var dynamicLibs []string
	for _, lib := range libs {
		linkType := libLinkTypeAuto
		for _, pattern := range p.ForceDynamicLinkPatterns {
			if wildcard.Match(pattern, lib) {
				linkType = libLinkTypeDynamic
				break
			}
		}

		for _, pattern := range p.ForceStaticLinkPatterns {
			if wildcard.Match(pattern, lib) {
				if linkType == libLinkTypeDynamic {
					return nil, "", -1, fmt.Errorf("library '%s' is forced to be both dynamically and statically linked", lib)
				}
				linkType = libLinkTypeStatic
				break
			}
		}

		switch linkType {
		case libLinkTypeAuto:
			autoLibs = append(autoLibs, lib)
		case libLinkTypeDynamic:
			dynamicLibs = append(dynamicLibs, lib)
		case libLinkTypeStatic:
			staticLibs = append(staticLibs, lib)
		default:
			return nil, "", -1, fmt.Errorf("unexpected linktype %v", linkType)
		}
	}

	if len(dynamicLibs) == 0 && len(staticLibs) == 0 {
		// is about linking, we do have rules about linking,
		// but apparently they do not affect anything, so
		// just passing-through as is.
		return p.runPkgConfig(args...)
	}

	var combinedErrorOutput []string

	alreadyHaveLib := map[string]struct{}{}
	var outputForStaticLibs []string
	if len(staticLibs) > 0 {
		args := make([]string, len(flags)+1+len(staticLibs))
		args[0] = "--static"
		copy(args[1:], flags)
		copy(args[len(flags)+1:], staticLibs)
		output, stdErr, exitCode, err := p.runPkgConfig(args...)
		if err != nil {
			return nil, stdErr, exitCode, fmt.Errorf("unable to get the config for the non-static/dynamic-forced libs: %w", err)
		}

		var processedOutput []string
		libCount := 0
		for _, word := range output {
			if !strings.HasPrefix(word, "-l") {
				processedOutput = append(processedOutput, word)
				continue
			}

			libName := "lib" + word[2:]

			forceDynamic := false
			for _, pattern := range p.ForceDynamicLinkPatterns {
				if wildcard.Match(pattern, libName) {
					forceDynamic = true
					break
				}
			}

			if forceDynamic {
				dynamicLibs = append(dynamicLibs, libName)
				continue
			}
			processedOutput = append(processedOutput, word)
			alreadyHaveLib[libName] = struct{}{}
			libCount++
		}
		if libCount != 0 {
			outputForStaticLibs = append([]string{"-Wl,-Bstatic"}, processedOutput...)
		}
		if len(stdErr) > 0 {
			combinedErrorOutput = append(combinedErrorOutput, stdErr)
		}
	}

	{
		var newDynamicLibs []string
		for _, lib := range dynamicLibs {
			if _, ok := alreadyHaveLib[lib]; !ok {
				newDynamicLibs = append(newDynamicLibs, lib)
			}
		}
		dynamicLibs = newDynamicLibs
	}

	var outputForDynamicLibs []string
	if len(dynamicLibs) > 0 {
		args := make([]string, len(flags)+1+len(dynamicLibs))
		args[0] = "--shared"
		copy(args[1:], flags)
		copy(args[len(flags)+1:], dynamicLibs)
		output, stdErr, exitCode, err := p.runPkgConfig(args...)
		if err != nil {
			return nil, stdErr, exitCode, fmt.Errorf("unable to get the config for the non-static/dynamic-forced libs: %w", err)
		}
		for _, word := range output {
			if strings.HasPrefix(word, "-l") {
				alreadyHaveLib["lib"+word[2:]] = struct{}{}
			}
		}
		outputForDynamicLibs = append([]string{"-Wl,-Bdynamic"}, output...)
		if len(stdErr) > 0 {
			combinedErrorOutput = append(combinedErrorOutput, stdErr)
		}
	}

	{
		var newAutoLibs []string
		for _, lib := range autoLibs {
			if _, ok := alreadyHaveLib[lib]; !ok {
				newAutoLibs = append(newAutoLibs, lib)
			}
		}
		autoLibs = newAutoLibs
	}

	var outputForAutoLibs []string
	if len(autoLibs) > 0 {
		args := make([]string, len(flags)+len(autoLibs))
		copy(args, flags)
		copy(args[len(flags):], autoLibs)
		output, stdErr, exitCode, err := p.runPkgConfig(args...)
		if err != nil {
			return nil, stdErr, exitCode, fmt.Errorf("unable to get the config for the non-static/dynamic-forced libs: %w", err)
		}
		outputForAutoLibs = output
		if len(stdErr) > 0 {
			combinedErrorOutput = append(combinedErrorOutput, stdErr)
		}
	}

	var combinedOutput []string
	combinedOutput = append(combinedOutput, outputForAutoLibs...)
	combinedOutput = append(combinedOutput, outputForStaticLibs...)
	combinedOutput = append(combinedOutput, outputForDynamicLibs...)

	return combinedOutput, strings.Join(combinedErrorOutput, "\n"), 0, nil
}

func (p *PkgConfig) runPkgConfig(args ...string) ([]string, string, int, error) {
	stdOut, stdErr, exitCode, err := p.CommandExecutor.Execute(pkgConfig, args...)
	return parsePkgConfigOutput(string(stdOut)), string(stdErr), exitCode, err
}

func parsePkgConfigOutput(s string) []string {
	return strings.Split(strings.Trim(s, "\n"), " ")
}
