package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/chai2010/gettext-go/gettext"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type profileCmd struct {
	httpAddr string
}

func (c *profileCmd) showByDefault() bool {
	return true
}

var profileEditHelp string = gettext.Gettext(
	"### This is a yaml representation of the profile.\n" +
		"### Any line starting with a '# will be ignored.\n" +
		"###\n" +
		"### A profile consists of a set of configuration items followed by a set of\n" +
		"### devices.\n" +
		"###\n" +
		"### An example would look like:\n" +
		"### name: onenic\n" +
		"### config:\n" +
		"###   raw.lxc: lxc.aa_profile=unconfined\n" +
		"### devices:\n" +
		"###   eth0:\n" +
		"###     nictype: bridged\n" +
		"###     parent: lxcbr0\n" +
		"###     type: nic\n" +
		"###\n" +
		"### Note that the name is shown but cannot be changed\n")

func (c *profileCmd) usage() string {
	return gettext.Gettext(
		"Manage configuration profiles.\n" +
			"\n" +
			"lxc profile list [filters]                     List available profiles\n" +
			"lxc profile show <profile>                     Show details of a profile\n" +
			"lxc profile create <profile>                   Create a profile\n" +
			"lxc profile edit <profile>                     Edit profile in external editor\n" +
			"lxc profile copy <profile> <remote>            Copy the profile to the specified remote\n" +
			"lxc profile set <profile> <key> <value>        Set profile configuration\n" +
			"lxc profile delete <profile>                   Delete a profile\n" +
			"lxc profile apply <container> <profiles>\n" +
			"    Apply a comma-separated list of profiles to a container, in order.\n" +
			"    All profiles passed in this call (and only those) will be applied\n" +
			"    to the specified container.\n" +
			"    Example: lxc profile apply foo default,bar # Apply default and bar\n" +
			"             lxc profile apply foo default # Only default is active\n" +
			"             lxc profile apply '' # no profiles are applied anymore\n" +
			"             lxc profile apply bar,default # Apply default second now\n" +
			"\n" +
			"Devices:\n" +
			"lxc profile device list <profile>              List devices in the given profile.\n" +
			"lxc profile device show <profile>              Show full device details in the given profile.\n" +
			"lxc profile device remove <profile> <name>     Remove a device from a profile.\n" +
			"lxc profile device add <profile name> <device name> <device type> [key=value]...\n" +
			"    Add a profile device, such as a disk or a nic, to the containers\n" +
			"    using the specified profile.\n")
}

func (c *profileCmd) flags() {}

func (c *profileCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	if args[0] == "list" {
		return doProfileList(config, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	remote, profile := config.ParseRemoteAndContainer(args[1])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	switch args[0] {
	case "create":
		return doProfileCreate(client, profile)
	case "delete":
		return doProfileDelete(client, profile)
	case "device":
		return doProfileDevice(config, args)
	case "edit":
		return doProfileEdit(client, profile)
	case "apply":
		container := profile
		switch len(args) {
		case 2:
			profile = ""
		case 3:
			profile = args[2]
		default:
			return errArgs
		}
		return doProfileApply(client, container, profile)
	case "get":
		return doProfileGet(client, profile, args[2:])
	case "set":
		return doProfileSet(client, profile, args[2:])
	case "unset":
		return doProfileSet(client, profile, args[2:])
	case "copy":
		return doProfileCopy(config, client, profile, args[2:])
	case "show":
		return doProfileShow(client, profile)
	default:
		return fmt.Errorf("unknown profile cmd %s", args[0])
	}
}

func doProfileCreate(client *lxd.Client, p string) error {
	err := client.ProfileCreate(p)
	if err == nil {
		fmt.Printf(gettext.Gettext("Profile %s created\n"), p)
	}
	return err
}

func doProfileEdit(client *lxd.Client, p string) error {
	if !terminal.IsTerminal(syscall.Stdin) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		newdata.Name = p
		return client.PutProfile(p, newdata)
	}

	profile, err := client.ProfileConfig(p)
	if err != nil {
		return err
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
	}
	data, err := yaml.Marshal(&profile)
	f, err := ioutil.TempFile("", "lxd_lxc_profile_")
	if err != nil {
		return err
	}
	fname := f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(fname)
		return err
	}
	f.Write([]byte(profileEditHelp))
	f.Write(data)
	f.Close()
	defer os.Remove(fname)

	for {
		cmdParts := strings.Fields(editor)
		cmd := exec.Command(cmdParts[0], append(cmdParts[1:], fname)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			return err
		}
		contents, err := ioutil.ReadFile(fname)
		if err != nil {
			return err
		}
		newdata := shared.ProfileConfig{}
		err = yaml.Unmarshal(contents, &newdata)
		newdata.Name = p
		if err != nil {
			fmt.Fprintf(os.Stderr, gettext.Gettext("YAML parse error %v\n"), err)
			fmt.Printf("Press enter to play again ")
			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			continue
		}
		err = client.PutProfile(p, newdata)
		if err != nil {
			return err
		}
		break
	}
	return nil
}

func doProfileDelete(client *lxd.Client, p string) error {
	err := client.ProfileDelete(p)
	if err == nil {
		fmt.Printf(gettext.Gettext("Profile %s deleted\n"), p)
	}
	return err
}

func doProfileApply(client *lxd.Client, c string, p string) error {
	resp, err := client.ApplyProfile(c, p)
	if err == nil {
		if p == "" {
			p = "(none)"
		}
		fmt.Printf(gettext.Gettext("Profile %s applied to %s\n"), p, c)
	} else {
		return err
	}
	return client.WaitForSuccess(resp.Operation)
}

func doProfileShow(client *lxd.Client, p string) error {
	profile, err := client.ProfileConfig(p)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	fmt.Printf("%s", data)

	return nil
}

func doProfileCopy(config *lxd.Config, client *lxd.Client, p string, args []string) error {
	if len(args) != 1 {
		return errArgs
	}
	remote, newname := config.ParseRemoteAndContainer(args[0])
	if newname == "" {
		newname = p
	}

	dest, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	return client.ProfileCopy(p, newname, dest)
}

func doProfileDevice(config *lxd.Config, args []string) error {
	// device add b1 eth0 nic type=bridged
	// device list b1
	// device remove b1 eth0
	if len(args) < 3 {
		return errArgs
	}
	switch args[1] {
	case "add":
		return deviceAdd(config, "profile", args)
	case "remove":
		return deviceRm(config, "profile", args)
	case "list":
		return deviceList(config, "profile", args)
	case "show":
		return deviceShow(config, "profile", args)
	default:
		return errArgs
	}
}

func doProfileGet(client *lxd.Client, p string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 1 {
		return errArgs
	}

	resp, err := client.GetProfileConfig(p)
	if err != nil {
		return err
	}
	for k, v := range resp {
		if k == args[0] {
			fmt.Printf("%s\n", v)
		}
	}
	return nil
}

func doProfileSet(client *lxd.Client, p string, args []string) error {
	// we shifted @args so so it should read "<key> [<value>]"
	if len(args) < 1 {
		return errArgs
	}

	key := args[0]
	var value string
	if len(args) < 2 {
		value = ""
	} else {
		value = args[1]
	}
	err := client.SetProfileConfigItem(p, key, value)
	return err
}

func doProfileList(config *lxd.Config, args []string) error {
	var remote string
	if len(args) > 1 {
		var name string
		remote, name = config.ParseRemoteAndContainer(args[1])
		if name != "" {
			return fmt.Errorf(gettext.Gettext("Cannot provide container name to list"))
		}
	} else {
		remote = config.DefaultRemote
	}

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	profiles, err := client.ListProfiles()
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", strings.Join(profiles, "\n"))
	return nil
}
