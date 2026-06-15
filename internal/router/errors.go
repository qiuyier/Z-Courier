package router

import "errors"

var ErrRouteNotFound = errors.New("router: route not found")

var ErrOverloaded = errors.New("router: overloaded")
