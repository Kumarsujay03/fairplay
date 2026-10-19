package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// A host firewall that drops incoming connections is the usual reason Wi-Fi
// guests can't open the link while the internet link works: the tunnel
// connects outwards, Wi-Fi guests connect inwards. FairPlay cannot change the
// firewall without the user's password, so it detects the situation, offers to
// run the one command that fixes it, and remembers when that was done.

type firewall struct {
	Kind    string `json:"kind"`              // ufw, firewalld, windows, macos, or "" for none detected
	Command string `json:"command,omitempty"` // what to run, shown to the user
	Note    string `json:"note,omitempty"`
	Done    bool   `json:"done"` // the user already ran FairPlay's fix for this port
	args    []string
}

// detectFirewall reports a firewall that is likely to block Wi-Fi guests.
func detectFirewall(port int, nets []string, exe string) firewall {
	switch runtime.GOOS {
	case "linux":
		if ufwBlocking() {
			// Only the local network(s) are opened, never the whole internet.
			var cmds []string
			for _, n := range nets {
				cmds = append(cmds, fmt.Sprintf("sudo ufw allow from %s to any port %d proto tcp comment FairPlay", n, port))
			}
			if len(cmds) == 0 {
				cmds = []string{fmt.Sprintf("sudo ufw allow %d/tcp comment FairPlay", port)}
			}
			return firewall{Kind: "ufw", Command: strings.Join(cmds, " && "), args: []string{"sh", "-c", strings.Join(cmds, " && ")}}
		}
		if out, err := exec.Command("systemctl", "is-active", "firewalld").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			c := fmt.Sprintf("sudo firewall-cmd --permanent --add-port=%d/tcp && sudo firewall-cmd --reload", port)
			return firewall{Kind: "firewalld", Command: c, args: []string{"sh", "-c", c}}
		}
	case "windows":
		rule := fmt.Sprintf(`netsh advfirewall firewall add rule name="FairPlay" dir=in action=allow program="%s" protocol=TCP localport=%d profile=private,domain enable=yes`, exe, port)
		ps := fmt.Sprintf(`Start-Process -Verb RunAs -Wait -FilePath netsh -ArgumentList 'advfirewall firewall add rule name="FairPlay" dir=in action=allow program="%s" protocol=TCP localport=%d profile=private,domain enable=yes'`, exe, port)
		return firewall{Kind: "windows", Command: rule + "   (in an administrator terminal)",
			Note: "Your Wi-Fi must be set to Private in Windows network settings, not Public.",
			args: []string{"powershell", "-NoProfile", "-Command", ps}}
	case "darwin":
		out, _ := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output()
		if strings.Contains(strings.ToLower(string(out)), "enabled") {
			fw := "/usr/libexec/ApplicationFirewall/socketfilterfw"
			c := fmt.Sprintf("sudo %s --add %q && sudo %s --unblockapp %q", fw, exe, fw, exe)
			return firewall{Kind: "macos", Command: c, args: []string{"sh", "-c", c}}
		}
	}
	return firewall{}
}

// ufwBlocking reads ufw's own config (world-readable) to see whether it is
// enabled with a default policy that drops incoming connections.
func ufwBlocking() bool {
	conf, err := os.ReadFile("/etc/ufw/ufw.conf")
	if err != nil || !strings.Contains(string(conf), "ENABLED=yes") {
		return false
	}
	def, err := os.ReadFile("/etc/default/ufw")
	if err != nil {
		return true // enabled, policy unknown: assume the safe default (drop)
	}
	for _, line := range strings.Split(string(def), "\n") {
		if strings.HasPrefix(line, "DEFAULT_INPUT_POLICY=") {
			v := strings.Trim(strings.TrimPrefix(line, "DEFAULT_INPUT_POLICY="), `"' `)
			return v != "ACCEPT"
		}
	}
	return true
}

// lanNets returns the local IPv4 networks (e.g. 192.168.1.0/24) FairPlay is reachable on.
func lanNets() []string {
	seen := map[string]bool{}
	var out []string
	for _, ifc := range usableInterfaces() {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			n := (&net.IPNet{IP: ipn.IP.Mask(ipn.Mask), Mask: ipn.Mask}).String()
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func firewallKey(fw firewall, port int) string { return fmt.Sprintf("%s:%d", fw.Kind, port) }

// apply runs the fix in this terminal. sudo (or the Windows UAC dialog) asks
// for permission itself.
func (fw firewall) apply() error {
	if len(fw.args) == 0 {
		return fmt.Errorf("nothing to do")
	}
	cmd := exec.Command(fw.args[0], fw.args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// offerFirewallFix asks once, on an interactive start, whether to let Wi-Fi
// guests through. It returns true if the fix was applied.
func offerFirewallFix(fw firewall, port int, s *settings, dataDir string) bool {
	key := firewallKey(fw, port)
	if fw.Kind == "" || s.FirewallOK == key || s.FirewallAsked == key || !stdinIsTerminal() || os.Getenv("FAIRPLAY_NO_PROMPT") != "" {
		return s.FirewallOK == key
	}
	s.FirewallAsked = key
	_ = saveSettings(dataDir, *s)
	fmt.Printf("\n  Your firewall (%s) blocks guests on your Wi-Fi from reaching FairPlay.\n", fw.Kind)
	fmt.Printf("  FairPlay can allow them now by running:\n    %s\n", fw.Command)
	if fw.Note != "" {
		fmt.Printf("  %s\n", fw.Note)
	}
	fmt.Print("  Allow Wi-Fi guests? [Y/n] ")
	reply, err := bufio.NewReader(os.Stdin).ReadString('\n')
	reply = strings.ToLower(strings.TrimSpace(reply))
	// Only a real Enter or "y" counts as yes; end of input never does.
	if err != nil || (reply != "" && reply != "y" && reply != "yes") {
		fmt.Println("  Skipped. Run `fairplay firewall` any time to do it later.")
		return false
	}
	if err := fw.apply(); err != nil {
		fmt.Printf("  That didn't work (%v). Run `fairplay firewall` to try again.\n", err)
		return false
	}
	s.FirewallOK = key
	_ = saveSettings(dataDir, *s)
	fmt.Println("  Done. Wi-Fi guests can join now.")
	return true
}

// runFirewall is `fairplay firewall`.
func runFirewall(o options) {
	exe, _ := os.Executable()
	fw := detectFirewall(o.port, lanNets(), exe)
	if fw.Kind == "" {
		fmt.Println("No firewall that blocks incoming connections was found, so Wi-Fi guests should be able to join.")
		fmt.Println("If they still can't, check that both devices are on the same network and that the router has no \"AP isolation\" or guest-network mode turned on.")
		return
	}
	fmt.Printf("Allowing Wi-Fi guests through %s on port %d:\n  %s\n", fw.Kind, o.port, fw.Command)
	if fw.Note != "" {
		fmt.Println(fw.Note)
	}
	if err := fw.apply(); err != nil {
		fail("could not change the firewall: %v", err)
	}
	if err := os.MkdirAll(o.data, 0o700); err == nil {
		s := loadSettings(o.data)
		s.FirewallOK = firewallKey(fw, o.port)
		_ = saveSettings(o.data, s)
	}
	fmt.Println("Done. Wi-Fi guests can join now.")
}
