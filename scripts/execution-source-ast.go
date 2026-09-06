package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type sourcePackage struct {
	vars map[string]ast.Expr
	funcs map[string]*ast.FuncDecl
	positions *token.FileSet
	root string
	active map[string]bool
}

func (source *sourcePackage) eval(expression ast.Expr, env map[string]any, depth int) any {
	if expression == nil || depth > 35 { return nil }
	switch node := expression.(type) {
	case *ast.BasicLit:
		if node.Kind == token.STRING { value, _ := strconv.Unquote(node.Value); return value }
		return node.Value
	case *ast.Ident:
		if value, ok := env[node.Name]; ok { return value }
		if node.Name == "true" { return true }; if node.Name == "false" { return false }
		if source.active[node.Name] { return nil }
		source.active[node.Name] = true
		value := source.eval(source.vars[node.Name], env, depth+1)
		delete(source.active, node.Name)
		return value
	case *ast.IndexExpr:
		if object,ok:=source.eval(node.X,env,depth+1).(map[string]any);ok { key,_:=source.eval(node.Index,env,depth+1).(string);return object[key] }
	case *ast.SelectorExpr:
		if base, ok := source.eval(node.X, env, depth+1).(map[string]any); ok { return base[node.Sel.Name] }
		if node.Sel.Name == "File" { return "file" }; if node.Sel.Name == "Stdin" { return "stdin" }
		return nil
	case *ast.BinaryExpr:
		left, leftOK := source.eval(node.X, env, depth+1).(string)
		right, rightOK := source.eval(node.Y, env, depth+1).(string)
		if node.Op == token.ADD && leftOK && rightOK { return left+right }
	case *ast.CompositeLit:
		object := map[string]any{}
		list := []any{}
		keyed := false
		for _, element := range node.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				keyed = true
				key := ""
				if identifier, ok := pair.Key.(*ast.Ident); ok { key = identifier.Name } else { key, _ = source.eval(pair.Key,env,depth+1).(string) }
				if key != "Execute" && key != "Validate" && key != "DryRun" && key != "Normalize" && key != "PostMount" && key != "PrintFlagSchema" { object[key] = source.eval(pair.Value, env, depth+1) }
			} else { list = append(list, source.eval(element, env, depth+1)) }
		}
		if keyed { return object }; return list
	case *ast.CallExpr:
		name := ""
		if identifier, ok := node.Fun.(*ast.Ident); ok { name = identifier.Name }
		if name == "append" || name == "concatFlags" {
			result := []any{}
			for _, argument := range node.Args {
				value := source.eval(argument, env, depth+1)
				if list, ok := value.([]any); ok { result = append(result,list...) } else if value != nil { result = append(result,value) }
			}
			return result
		}
		if name == "withExtraTips" { return source.eval(node.Args[0],env,depth+1) }
		if function, ok := source.funcs[name]; ok {
			local := map[string]any{}
			for key,value := range env { local[key] = value }
			index := 0
			for _, parameter := range function.Type.Params.List {
				for _, parameterName := range parameter.Names {
					if index < len(node.Args) { local[parameterName.Name] = source.eval(node.Args[index],env,depth+1) }; index++
				}
			}
			return source.body(function.Body,local,depth+1)
		}
	}
	return nil
}

func (source *sourcePackage) body(body *ast.BlockStmt, env map[string]any, depth int) any {
	if body == nil { return nil }
	for _, statement := range body.List {
		switch node := statement.(type) {
		case *ast.AssignStmt:
			for index, left := range node.Lhs {
				if index >= len(node.Rhs) { continue }
				if name, ok := left.(*ast.Ident); ok { env[name.Name] = source.eval(node.Rhs[index],env,depth+1) }
				if field,ok:=left.(*ast.SelectorExpr);ok {if object,ok:=source.eval(field.X,env,depth+1).(map[string]any);ok {object[field.Sel.Name]=source.eval(node.Rhs[index],env,depth+1)}}
			}
		case *ast.ReturnStmt:
			if len(node.Results)>0 { return source.eval(node.Results[0],env,depth+1) }
		}
	}
	return nil
}

func main() {
	if os.Args[1]=="--engine" {
		positions:=token.NewFileSet()
		source:=sourcePackage{vars:map[string]ast.Expr{},funcs:map[string]*ast.FuncDecl{},positions:positions,active:map[string]bool{}}
		parsed,err:=parser.ParseFile(positions,filepath.Join(os.Args[2],"catalog.go"),nil,0);if err!=nil{panic(err)}
		for _,decl:=range parsed.Decls {if general,ok:=decl.(*ast.GenDecl);ok {for _,spec:=range general.Specs {if value,ok:=spec.(*ast.ValueSpec);ok {for index,name:=range value.Names {if index<len(value.Values){source.vars[name.Name]=value.Values[index]}}}}}}
		result:=map[string]any{}
		for _,name:=range []string{"apiSpecifications","legacySpecifications"} {result[name]=source.eval(source.vars[name],map[string]any{},0)}
		if err:=json.NewEncoder(os.Stdout).Encode(result);err!=nil{panic(err)}
		return
	}
	root := os.Args[1]
	register, err := os.ReadFile(filepath.Join(root,"shortcuts/register.go")); if err != nil { panic(err) }
	rows := []map[string]any{}
	directories, _ := filepath.Glob(filepath.Join(root,"shortcuts","*"))
	for _, directory := range directories {
		if !strings.Contains(string(register), "/shortcuts/"+filepath.Base(directory)+"\"") { continue }
		source := sourcePackage{vars:map[string]ast.Expr{},funcs:map[string]*ast.FuncDecl{},positions:token.NewFileSet(),root:root,active:map[string]bool{}}
		files, _ := filepath.Glob(filepath.Join(directory,"*.go"))
		for _, file := range files {
			if strings.HasSuffix(file,"_test.go") { continue }
			parsed, err := parser.ParseFile(source.positions,file,nil,0); if err != nil { panic(err) }
			for _, declaration := range parsed.Decls {
				switch node := declaration.(type) {
				case *ast.GenDecl:
					for _, spec := range node.Specs { if values, ok := spec.(*ast.ValueSpec); ok { for index,name := range values.Names { if index<len(values.Values) { source.vars[name.Name]=values.Values[index] } } } }
				case *ast.FuncDecl:
					if node.Recv==nil { source.funcs[node.Name.Name]=node }
				}
			}
		}
		function := source.funcs["Shortcuts"]
		if function==nil { continue }
		registered := map[string]bool{}
		var collect func(ast.Node)
		collect = func(node ast.Node) {
			ast.Inspect(node,func(child ast.Node)bool {
				if identifier,ok:=child.(*ast.Ident);ok { if expression,exists:=source.vars[identifier.Name];exists&&!registered[identifier.Name] { registered[identifier.Name]=true; collect(expression) } }
				return true
			})
		}
		collect(function.Body)
		registeredRows,_:=source.body(function.Body,map[string]any{},0).([]any)
		seen:=map[string]bool{}
		for _, raw := range registeredRows {
			value,ok:=raw.(map[string]any)
			if !ok || value["Service"]==nil || value["Command"]==nil { continue }
			path:=fmt.Sprint(value["Service"])+" "+fmt.Sprint(value["Command"])
			if seen[path] { continue };seen[path]=true
			name:=""
			candidates:=[]string{}
			for candidate:=range source.vars {candidates=append(candidates,candidate)}
			sort.Strings(candidates)
			for _,candidate:=range candidates {
				expression:=source.vars[candidate]
				candidateValue,ok:=source.eval(expression,map[string]any{},0).(map[string]any)
				if ok && candidateValue["Service"]==value["Service"] && candidateValue["Command"]==value["Command"] {name=candidate;break}
			}
			if name=="" {panic("unresolved registered shortcut "+path)}
			position:=source.positions.Position(source.vars[name].Pos())
			file,_:=filepath.Rel(root,position.Filename)
			value["source"]=filepath.ToSlash(file); value["line"]=position.Line;value["variable"]=name
			rows=append(rows,value)
		}
	}
	sort.Slice(rows,func(left,right int)bool{return fmt.Sprint(rows[left]["Service"],rows[left]["Command"])<fmt.Sprint(rows[right]["Service"],rows[right]["Command"])})
	if err:=json.NewEncoder(os.Stdout).Encode(rows);err!=nil{panic(err)}
}
