export struct Counter {
    n: int
}

Counter.Secret(&self): int {
    return self.n
}

Counter.Pick[X](&self, x: X): X {
    return x
}

export Counter.Get(&self): int {
    return self.Secret() + Counter.Secret(self) + Twice(self) + Counter.Pick[int](self, 0)
}

export type Same = Counter

export interface Named {
    Name(&self): int
}

impl Named for Counter {
    Name(&self): int {
        return 1
    }
}

export struct Down {
    n: int
}

export Start(): Down {
    return Down(n: 3)
}

Down.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}
