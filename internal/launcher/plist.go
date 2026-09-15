package launcher

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

const searchPath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"

// plist renders the launchd agent property list.
func plist(p Paths, dashboard bool) []byte {
	arguments := []string{p.Binary(), "run", "-config", p.ConfigFile()}
	if dashboard {
		arguments = append(arguments, "-dashboard")
	}
	var b bytes.Buffer
	str := func(s string) string {
		var escaped bytes.Buffer
		_ = xml.EscapeText(&escaped, []byte(s))
		return escaped.String()
	}
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, argument := range arguments {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", str(argument))
	}
	b.WriteString("\t</array>\n")
	fmt.Fprintf(&b, "\t<key>WorkingDirectory</key>\n\t<string>%s</string>\n", str(p.State))
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<true/>\n")
	b.WriteString("\t<key>ThrottleInterval</key>\n\t<integer>5</integer>\n")
	fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n", str(p.LogFile()))
	fmt.Fprintf(&b, "\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", str(p.LogFile()))
	fmt.Fprintf(&b, "\t<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n\t\t<string>%s</string>\n\t</dict>\n", searchPath)
	b.WriteString("\t<key>Umask</key>\n\t<integer>63</integer>\n") // 0o077
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}
