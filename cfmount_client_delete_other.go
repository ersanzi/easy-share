//go:build !windows

package main

import "easyshare/internal/drive"

func (a *App) deleteNativeCloudKey(string) (bool, error)                    { return false, nil }
func (a *App) restoreNativeCloudResult(*drive.RecycleOperationResult) error { return nil }
