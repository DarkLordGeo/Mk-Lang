import std.vm

export Page(): int {
    unsafe "a query of the page size" {
        return vm.PageSize()
    }
}

export CodeSize(): int {
    unsafe "a code region made and released here, never entered" {
        c := vm.CodeRegion.New(64) else {
            return 0
        }
        n := c.Size()
        c.Release()
        return n
    }
}

export Reserve(): int {
    unsafe "a region reserved, committed and released here, never read" {
        r := vm.Reserve(1 << 20, vm.PageSize()) else {
            return 0
        }
        r.Commit(0, vm.PageSize()) else {
            r.Release()
            return 0
        }
        n := r.Size()
        r.Release()
        return n
    }
}
