export function hello(name: string): string {
  return "hello " + name;
}

export interface Greeter {
  greet(name: string): string;
}

export class EnglishGreeter implements Greeter {
  greet(name: string): string { return hello(name); }
}

export class SpanishGreeter implements Greeter {
  greet(name: string): string { return "hola " + name; }
}

export function chainA(name: string): string { return chainB(name); }
export function chainB(name: string): string { return chainC(name); }
export function chainC(name: string): string { return hello(name); }

export function source(): string { return userInput(); }
function userInput(): string { return "user"; }
export function sink(v: string): void { console.log(v); }
export function taintFlow(): void { sink(source()); }

export function clonePairA(x: number, y: number): number { return x + y + 1; }
export function clonePairB(x: number, y: number): number { return x + y + 1; }
