module github.com/wabarc/telegra.ph

go 1.15

require (
	github.com/PuerkitoBio/goquery v1.7.1
	github.com/gabriel-vasile/mimetype v1.3.1
	github.com/go-shiori/go-readability v0.0.0-20210627123243-82cc33435520
	github.com/go-shiori/obelisk v0.0.0-20201115143556-8de0d40b0a9b
	github.com/google/uuid v1.3.0 // indirect
	github.com/kallydev/telegraph-go v1.0.0
	github.com/oliamb/cutter v0.2.2
	github.com/pkg/errors v0.9.1
	github.com/wabarc/helper v0.0.0-20211225065210-3d35291efe54
	github.com/wabarc/imgbb v1.0.0
	github.com/wabarc/logger v0.0.0-20210730133522-86bd3f31e792
	github.com/wabarc/screenshot v1.3.1
	golang.org/x/net v0.0.0-20211216030914-fe4d6282115f
)

replace github.com/go-shiori/obelisk => github.com/wabarc/obelisk v0.0.0-20220214133717-b731766a194b
