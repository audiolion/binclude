// Package bincludegen generates the binclude.go file
package bincludegen

import (
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lu4p/binclude"
)

var (
	operatingSystems = []string{"linux", "windows", "darwin", "freebsd", "js", "plan9", "freebsd", "dragonfly", "openbsd", "solaris", "aix", "android"}
	archs            = []string{"ppc64", "386", "amd64", "wasm", "arm", "ppc64le", "mips", "mips64", "mips64le", "mipsle", "s390x", "arm64"}

	fset *token.FileSet

	gzip bool
)

func init() {
	flag.BoolVar(&gzip, "gzip", false, "compress files with gzip")
}

// Main1 gets called by cmd/binclude for code generation
func Main1() int {
	flag.Parse()
	compress := binclude.None
	if gzip {
		compress = binclude.Gzip
	}

	log.SetPrefix("[binclude] ")

	err := Generate(compress)
	if err != nil {
		log.Println("failed:", err)
		return 1
	}

	return 0
}

type goFile struct {
	path    string
	astFile *ast.File
}

// Generate a binclude.go file for the current working directory
func Generate(compress binclude.Compression) error {
	paths, _ := filepath.Glob("*.go")

	if len(paths) == 0 {
		return errors.New("No .go files found in current working directory")
	}

	fset = token.NewFileSet()

	var goFiles []goFile
	for _, path := range paths {
		if strings.HasSuffix(path, "binclude.go") {
			continue
		}

		astFile, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		goFiles = append(goFiles, goFile{
			path:    path,
			astFile: astFile,
		})
	}

	pkgName := goFiles[0].astFile.Name

	includedFiles, err := detectIncluded(goFiles)
	if err != nil {
		return err
	}

	fileSystems, err := buildFS(includedFiles)
	if err != nil {
		return err
	}

	for _, fs := range fileSystems {
		if err := fs.Encode(compress); err != nil {
			return err
		}
	}

	return generateFiles(pkgName, fileSystems)
}

func buildFS(includedFiles []includedFile) (map[string]*binclude.FileSystem, error) {
	const bincludeName = "binclude"
	fileSystems := make(map[string]*binclude.FileSystem)
	var buildTag string

	fileSystems["default"] = &binclude.FileSystem{}
	fileSystems["default"].Files = make(binclude.Files)

	var walkFn filepath.WalkFunc = func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		var content []byte

		if !info.IsDir() {
			content, err = ioutil.ReadFile(path)
			if err != nil {
				return err
			}

		}

		path = filepath.ToSlash(path)

		if fileSystems[buildTag] == nil {
			fileSystems[buildTag] = &binclude.FileSystem{}
			fileSystems[buildTag].Files = make(binclude.Files)
		}

		fileSystems[buildTag].Files[path] = &binclude.File{
			Filename: info.Name(),
			Mode:     info.Mode(),
			ModTime:  info.ModTime(),
			Content:  content,
		}

		return nil
	}

	for _, file := range includedFiles {
		buildTag = ""

		for _, arch := range archs {
			if strings.HasSuffix(file.goFile, arch+".go") {
				buildTag = "_" + arch
			}
		}

		for _, sys := range operatingSystems {
			if strings.HasSuffix(file.goFile, sys+buildTag+".go") {
				buildTag = "_" + sys + buildTag
			}
		}

		if len(buildTag) == 0 {
			buildTag = "default"
		}

		err := filepath.Walk(file.includedPath, walkFn)
		if err != nil {
			return nil, err
		}
	}

	return fileSystems, nil
}

type includedFile struct {
	includedPath, goFile string
}

func detectIncluded(goFiles []goFile) ([]includedFile, error) {
	var includedFiles []includedFile

	var currentGoFile string

	visit := func(node ast.Node) bool {
		if node == nil {
			return true
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		v, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		if !(sel.Sel.Name == "Include" || sel.Sel.Name == "IncludeFromFile") || v.Name != "binclude" {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			log.Fatalln("argument is not string literal")
		}

		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			log.Fatalln("cannot unquote string:", err)
		}

		if sel.Sel.Name == "IncludeFromFile" {
			content, err := ioutil.ReadFile(value)
			if err != nil {
				log.Fatalln("cannot read includefile:", value, "err:", err)
			}

			paths := strings.Split(string(content), "\n")
			for i := 0; i < len(paths); i++ {
				paths[i] = strings.TrimSpace(paths[i])
				if paths[i] == "" {
					paths = remove(paths, i)
					i-- // reset positon by one because an element was removed
				}
			}

			for _, path := range paths {
				includedFiles = append(includedFiles, includedFile{
					goFile:       currentGoFile,
					includedPath: path,
				})
			}

			return true
		}

		includedFiles = append(includedFiles, includedFile{
			goFile:       currentGoFile,
			includedPath: value,
		})

		return true
	}

	for _, file := range goFiles {
		currentGoFile = file.path
		ast.Inspect(file.astFile, visit)
	}

	for i, file := range includedFiles {
		var err error

		if filepath.IsAbs(file.includedPath) {
			return nil, errors.New("only supports relative include paths")
		}

		_, err = os.Stat(file.includedPath)
		if err != nil {
			return nil, err
		}

		includedFiles[i].includedPath = strings.TrimPrefix(file.includedPath, "./")
	}

	return includedFiles, nil
}

func remove(slice []string, s int) []string {
	return append(slice[:s], slice[s+1:]...)
}
