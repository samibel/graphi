package main

import (
	"fmt"
	"os"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func main() {
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	lang := grammars.RubyLanguage()
	parser := gts.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	root := tree.RootNode()
	printTree(root, lang, src, 0)
}

func printTree(n *gts.Node, lang *gts.Language, src []byte, depth int) {
	if n == nil {
		return
	}
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	startRow, startCol := n.StartPoint().Row, n.StartPoint().Column
	endRow, endCol := n.EndPoint().Row, n.EndPoint().Column
	typ := n.Type(lang)
	if typ == "method" || typ == "class" || typ == "module" || typ == "body_statement" || typ == "program" || typ == "call" || typ == "identifier" {
		fmt.Printf("%s%s [%d:%d-%d:%d]\n", indent, typ, startRow, startCol, endRow, endCol)
	}
	for i := 0; i < n.ChildCount(); i++ {
		printTree(n.Child(i), lang, src, depth+1)
	}
}