# Records demo.gif. Run via ./hack/demo.sh, which starts the SSH server the
# the session pane connects to. https://github.com/charmbracelet/vhs

Output demo.gif

Set Shell zsh
Set FontSize 14
Set Width 1180
Set Height 620
Set Padding 18
Set Theme "TokyoNight"
Set TypingSpeed 55ms

Hide
Type "clear && /tmp/omassh-demo/omassh -db /tmp/omassh-demo/omassh.db -o UserKnownHostsFile=/tmp/omassh-demo/known_hosts" Enter
Sleep 3s
Show

Sleep 1200ms

# reachability across the selected group
Type "p"     Sleep 2200ms

# a group whose hosts inherit a user and a key
Type "1"     Sleep 600ms
Type "j"     Sleep 1200ms
Type "2"     Sleep 900ms

# editing a host: jump host, group and tags are all picked from a list
Type "e"     Sleep 1200ms
Tab Tab Tab Tab Tab
Sleep 600ms
Down         Sleep 1600ms
Escape       Sleep 400ms
Tab          Sleep 600ms
Down         Sleep 1200ms
Space        Sleep 900ms
Down Down
Space        Sleep 1400ms
Enter        Sleep 700ms
Escape       Sleep 900ms

# the headline: t opens the session in the main pane
Type "t"     Sleep 3500ms
Type "uptime" Enter
Sleep 1800ms

# the prefix hands the keyboard back; the session stays connected beside it
Ctrl+\
Type "w"     Sleep 1800ms

# another host takes the pane
Type "j"     Sleep 600ms
Type "t"     Sleep 3000ms
Type "df -h /" Enter
Sleep 2000ms

Ctrl+\
Type "w"     Sleep 1500ms

Type "?"     Sleep 2500ms
Escape       Sleep 500ms
Type "q"     Sleep 1000ms
