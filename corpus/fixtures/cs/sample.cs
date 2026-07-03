using System;

public static class Fixture
{
    public static string Hello(string name) => "hello " + name;

    public interface IGreeter
    {
        string Greet(string name);
    }

    public class EnglishGreeter : IGreeter
    {
        public string Greet(string name) => Hello(name);
    }

    public class SpanishGreeter : IGreeter
    {
        public string Greet(string name) => "hola " + name;
    }

    public static string ChainA(string name) => ChainB(name);
    public static string ChainB(string name) => ChainC(name);
    public static string ChainC(string name) => Hello(name);

    public static string Source() => UserInput();
    public static string UserInput() => "user";
    public static void Sink(string v) { }

    public static void TaintFlow()
    {
        Sink(Source());
    }

    public static int ClonePairA(int x, int y) => x + y + 1;
    public static int ClonePairB(int x, int y) => x + y + 1;
}
