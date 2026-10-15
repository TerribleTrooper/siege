// Package siege вшивает фронтенд в бинарник: на VM уезжает один файл,
// без отдельной раздачи статики.
package siege

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var embedded embed.FS

// WebFS отдаёт содержимое каталога web как корень файловой системы.
func WebFS() fs.FS {
	sub, err := fs.Sub(embedded, "web")
	if err != nil {
		panic("siege: не удалось открыть вшитый web/: " + err.Error())
	}
	return sub
}
