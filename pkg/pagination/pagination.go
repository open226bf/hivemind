package pagination

const (
	DefaultPage  = 1
	DefaultLimit = 20
	MaxLimit     = 100
)

type Page struct {
	Number int
	Size   int
}

func New(number, size int) Page {
	if number < 1 {
		number = DefaultPage
	}
	if size < 1 {
		size = DefaultLimit
	}
	if size > MaxLimit {
		size = MaxLimit
	}
	return Page{Number: number, Size: size}
}

func (p Page) Offset() int {
	return (p.Number - 1) * p.Size
}
