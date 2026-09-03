# A VHS tape for the README demo: vhs demo.tape
# https://github.com/charmbracelet/vhs

Output demo.gif

Set Shell zsh
Set FontSize 15
Set Width 1200
Set Height 700
Set Padding 20
Set Theme "TokyoNight"

Hide
Type "go build -o /tmp/omassh-demo ./cmd/omassh && /tmp/omassh-demo -db /tmp/omassh-demo.db" Enter
Sleep 3s
Show

Sleep 1s
Type "j"       Sleep 700ms
Type "j"       Sleep 700ms
Type "p"       Sleep 2s
Type "/"       Sleep 500ms
Type "web"     Sleep 1500ms
Escape         Sleep 700ms
Type "3"       Sleep 1s
Type "2"       Sleep 700ms
Type "?"       Sleep 3s
Escape         Sleep 700ms
Type "q"       Sleep 1s
