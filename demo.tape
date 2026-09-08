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
Type "clear && /tmp/omassh-demo/omassh -db /tmp/omassh-demo/omassh.db -secrets=memory -o UserKnownHostsFile=/tmp/omassh-demo/known_hosts" Enter
Sleep 3s
Show

Sleep 1200ms

# reachability across the selected group
Type "p"     Sleep 2200ms

# a group whose hosts inherit a user and a key
Type "1"     Sleep 600ms
Type "j"     Sleep 1200ms
Type "2"     Sleep 900ms

# the headline: enter opens the session in the main pane
Enter        Sleep 3500ms
Type "uptime" Enter
Sleep 1800ms

# the prefix hands the keyboard back; the session stays connected beside it
Ctrl+\
Type "w"     Sleep 1800ms

# another host takes the pane
Type "j"     Sleep 600ms
Enter        Sleep 3000ms
Type "df -h /" Enter
Sleep 2000ms

Ctrl+\
Type "w"     Sleep 1500ms

Type "?"     Sleep 2500ms
Escape       Sleep 500ms
Type "q"     Sleep 1000ms
