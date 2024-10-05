package utils

import (
	"fmt"
	"reflect"

	"github.com/gotk3/gotk3/gtk"
)

func BuilderGetObject[T any](builder *gtk.Builder, name string) (obj T, err error) {
	gtkObject, err := builder.GetObject(name)
	if err != nil {
		return
	}

	goObj, ok := gtkObject.(T)
	if !ok {
		err = fmt.Errorf("Builder object '%s' is type %v", name, reflect.TypeOf(goObj))
		return
	}

	return goObj, nil
}

func ModelGetValue[T any](model *gtk.TreeModel, iter *gtk.TreeIter, col int) (obj T, err error) {
	id, err := model.GetValue(iter, col)
	if err != nil {
		return
	}

	goObj, err := id.GoValue()
	if err != nil {
		return
	}

	obj, ok := goObj.(T)
	if !ok {
		err = fmt.Errorf("Model value in col '%d' is type %v", col, reflect.TypeOf(goObj))
		return
	}

	return
}
