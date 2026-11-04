// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

//go:build linux

package executor

import (
	"os"
	"strings"
	"testing"
)

func TestELFResolverUsesRegularSymbolTable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newELFResolver(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.close()
	resolver.loadSymbols()

	for _, symbol := range resolver.symbols {
		if !strings.Contains(symbol.Name, "TestELFResolverUsesRegularSymbolTable") {
			continue
		}
		name, _, _, ok := resolver.resolve(perfFrame{IP: symbol.Value, DSO: executable})
		if !ok || name == "" {
			t.Fatalf("failed to resolve symbol at %#x", symbol.Value)
		}
		return
	}
	t.Fatal("test function not present in ELF symbol table")
}
